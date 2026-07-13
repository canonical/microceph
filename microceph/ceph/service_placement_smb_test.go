package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// validSMBPayload is the reference mgr/smb SMBSpec JSON from the design.
const validSMBPayload = `{
  "service_type": "smb",
  "service_id": "dev",
  "placement": {"hosts": ["smbdev-1", "smbdev-2", "smbdev-3"]},
  "cluster_id": "dev",
  "features": ["clustered"],
  "config_uri": "rados://.smb/dev/scc.dev.json",
  "user_sources": ["rados://.smb/dev/users.dev.json"],
  "cluster_meta_uri": "rados://.smb/dev/cluster.meta.json",
  "cluster_lock_uri": "rados://.smb/dev/cluster.meta.lock",
  "cluster_public_addrs": [
    {"address": "10.105.154.245/24", "destination": null}
  ]
}`

type servicePlacementSMBSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestServicesPlacementSMB(t *testing.T) {
	suite.Run(t, new(servicePlacementSMBSuite))
}

// Set up test suite
func (s *servicePlacementSMBSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.TestStateInterface = mocks.NewStateInterface(s.T())
	// Bypass database-dependent ceph.conf rendering: these tests exercise the
	// SMB placement pipeline with a mock state, not a real cluster database.
	updateConfigFunc = func(_ context.Context, _ interfaces.StateInterface) error { return nil }
}

func (s *servicePlacementSMBSuite) TearDownTest() {
	updateConfigFunc = UpdateConfig
}

// populated returns an SMBServicePlacement loaded from a payload that must
// parse successfully.
func (s *servicePlacementSMBSuite) populated(payload string) *SMBServicePlacement {
	smb := &SMBServicePlacement{}
	err := smb.PopulateParams(s.TestStateInterface, payload)
	assert.NoError(s.T(), err)
	return smb
}

func (s *servicePlacementSMBSuite) TestHandlerWiring() {
	payload := types.EnableService{
		Name:    "smb",
		Wait:    true,
		Payload: `{"cluster_id":""}`,
	}

	// Proves the "smb" placement table entry exists and PopulateParams
	// errors propagate through the handler.
	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "not a valid ID")
}

func (s *servicePlacementSMBSuite) TestInvalidClusterID() {
	smb := &SMBServicePlacement{}

	for _, id := range []string{"", "-foo", "foo-", "foo_bar", strings.Repeat("a", 19)} {
		err := smb.PopulateParams(s.TestStateInterface, fmt.Sprintf(`{"cluster_id":"%s"}`, id))
		assert.ErrorContains(s.T(), err, "not a valid ID", "cluster_id: %q", id)
	}

	// Boundary: 18 alphanumeric chars is the upstream maximum.
	err := smb.PopulateParams(s.TestStateInterface, fmt.Sprintf(`{"cluster_id":"%s"}`, strings.Repeat("a", 18)))
	assert.NoError(s.T(), err)
}

func (s *servicePlacementSMBSuite) TestDomainFeatureRejected() {
	smb := &SMBServicePlacement{}
	err := smb.PopulateParams(s.TestStateInterface, `{"cluster_id":"dev","features":["domain"]}`)
	assert.ErrorContains(s.T(), err, "domain")
}

func (s *servicePlacementSMBSuite) TestCephfsProxyFeatureRejectedWithHint() {
	smb := &SMBServicePlacement{}
	err := smb.PopulateParams(s.TestStateInterface, `{"cluster_id":"dev","features":["clustered","cephfs-proxy"]}`)
	assert.ErrorContains(s.T(), err, "samba-vfs/new")
}

func (s *servicePlacementSMBSuite) TestUnknownFeatureRejected() {
	smb := &SMBServicePlacement{}
	err := smb.PopulateParams(s.TestStateInterface, `{"cluster_id":"dev","features":["wormholes"]}`)
	assert.ErrorContains(s.T(), err, "wormholes")
}

func (s *servicePlacementSMBSuite) TestUnsupportedFieldsRejected() {
	smb := &SMBServicePlacement{}

	for _, field := range []string{"bind_addrs", "custom_ports", "custom_dns", "remote_control_uri"} {
		payload := fmt.Sprintf(`{"cluster_id":"dev","%s":["x"]}`, field)
		err := smb.PopulateParams(s.TestStateInterface, payload)
		assert.ErrorContains(s.T(), err, field)
	}
}

func (s *servicePlacementSMBSuite) TestUnsetUnsupportedFieldsTolerated() {
	// mgr serialization may emit unset fields as null or empty; those must
	// not be rejected.
	smb := &SMBServicePlacement{}
	err := smb.PopulateParams(s.TestStateInterface,
		`{"cluster_id":"dev","bind_addrs":null,"custom_dns":[],"custom_ports":{}}`)
	assert.NoError(s.T(), err)
}

func (s *servicePlacementSMBSuite) TestValidSpecAccepted() {
	smb := s.populated(validSMBPayload)
	assert.Equal(s.T(), "dev", smb.Spec.ClusterID)
	assert.Equal(s.T(), []string{"smbdev-1", "smbdev-2", "smbdev-3"}, smb.Spec.Placement.Hosts)
}

// withSMBGroupMembership patches the grouped-services query to report the
// given rows for this host, and the port check to report available.
func (s *servicePlacementSMBSuite) withHospitalityEnv(rows []database.GroupedService, portFree bool) func() {
	db := mocks.NewGroupedServiceQueryIntf(s.T())
	if portFree {
		db.On("GetGroupedServicesOnHost", context.Background(), s.TestStateInterface).Return(rows, nil).Maybe()
	}

	originalDB := database.GroupedServicesQuery
	originalPortCheck := isAddressAvailableFunc
	database.GroupedServicesQuery = db
	isAddressAvailableFunc = func(address string) (bool, error) { return portFree, nil }

	return func() {
		database.GroupedServicesQuery = originalDB
		isAddressAvailableFunc = originalPortCheck
	}
}

func (s *servicePlacementSMBSuite) TestHospitalityFreeNode() {
	restore := s.withHospitalityEnv([]database.GroupedService{}, true)
	defer restore()

	smb := s.populated(validSMBPayload)
	assert.NoError(s.T(), smb.HospitalityCheck(context.Background(), s.TestStateInterface))
}

func (s *servicePlacementSMBSuite) TestHospitalityPortBusy() {
	restore := s.withHospitalityEnv(nil, false)
	defer restore()

	smb := s.populated(validSMBPayload)
	assert.ErrorContains(s.T(), smb.HospitalityCheck(context.Background(), s.TestStateInterface), "445")
}

func (s *servicePlacementSMBSuite) TestHospitalityNodeInOtherGroup() {
	restore := s.withHospitalityEnv([]database.GroupedService{
		{Member: "smbdev-1", Service: "smb", GroupID: "other"},
	}, true)
	defer restore()

	smb := s.populated(validSMBPayload)
	assert.ErrorContains(s.T(), smb.HospitalityCheck(context.Background(), s.TestStateInterface), "node-disjoint")
}

func (s *servicePlacementSMBSuite) TestHospitalityReApplySameGroup() {
	restore := s.withHospitalityEnv([]database.GroupedService{
		{Member: "smbdev-1", Service: "smb", GroupID: "dev"},
	}, true)
	defer restore()

	smb := s.populated(validSMBPayload)
	assert.ErrorContains(s.T(), smb.HospitalityCheck(context.Background(), s.TestStateInterface), "already a member of smb cluster 'dev'")
}

func (s *servicePlacementSMBSuite) TestHospitalityIgnoresOtherServices() {
	restore := s.withHospitalityEnv([]database.GroupedService{
		{Member: "smbdev-1", Service: "nfs", GroupID: "dev"},
	}, true)
	defer restore()

	smb := s.populated(validSMBPayload)
	assert.NoError(s.T(), smb.HospitalityCheck(context.Background(), s.TestStateInterface))
}

func (s *servicePlacementSMBSuite) TestDBUpdate() {
	smb := s.populated(validSMBPayload)

	db := mocks.NewGroupedServiceQueryIntf(s.T())
	ctx := context.Background()
	db.On("AddNew", []interface{}{ctx, s.TestStateInterface, "smb", "dev",
		json.RawMessage(validSMBPayload), database.SMBServiceInfo{}}...).Return(nil).Once()

	originalDB := database.GroupedServicesQuery
	defer func() { database.GroupedServicesQuery = originalDB }()
	database.GroupedServicesQuery = db

	assert.NoError(s.T(), smb.DbUpdate(ctx, s.TestStateInterface))
}
