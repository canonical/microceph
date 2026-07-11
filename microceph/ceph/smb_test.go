package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// mustCompactJSON compacts a JSON document, panicking on invalid input.
func mustCompactJSON(in string) string {
	var buf bytes.Buffer
	err := json.Compact(&buf, []byte(in))
	if err != nil {
		panic(err)
	}
	return buf.String()
}

type smbSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface

	enabled     []string
	disabled    []string
	regenerated []string
}

func TestSMBSuite(t *testing.T) {
	suite.Run(t, new(smbSuite))
}

// SetupTest wires recorder seams so orchestration tests observe exact
// per-node enable/disable sets without running the placement flow.
func (s *smbSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.TestStateInterface = mocks.NewStateInterface(s.T())

	s.enabled = nil
	s.disabled = nil
	s.regenerated = nil

	originalMembers := smbClusterMembersFunc
	originalEnable := smbEnableNodeFunc
	originalDisable := smbDisableNodeFunc
	originalRegenerate := smbRegenerateNodeFunc
	s.T().Cleanup(func() {
		smbClusterMembersFunc = originalMembers
		smbEnableNodeFunc = originalEnable
		smbDisableNodeFunc = originalDisable
		smbRegenerateNodeFunc = originalRegenerate
	})

	smbClusterMembersFunc = func(s interfaces.StateInterface) ([]string, error) {
		return []string{"m1", "m2", "m3"}, nil
	}
	smbEnableNodeFunc = func(ctx context.Context, st interfaces.StateInterface, node, payload string) error {
		s.enabled = append(s.enabled, node)
		return nil
	}
	smbDisableNodeFunc = func(ctx context.Context, st interfaces.StateInterface, node, clusterID string) error {
		s.disabled = append(s.disabled, node)
		return nil
	}
	smbRegenerateNodeFunc = func(ctx context.Context, st interfaces.StateInterface, node, clusterID string) error {
		s.regenerated = append(s.regenerated, node)
		return nil
	}
}

// withDB patches the grouped-services query with a fresh mock and restores
// the original on test cleanup.
func (s *smbSuite) withDB() *mocks.GroupedServiceQueryIntf {
	db := mocks.NewGroupedServiceQueryIntf(s.T())
	originalDB := database.GroupedServicesQuery
	s.T().Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db
	return db
}

func smbPayload(hosts string, count int) string {
	placement := fmt.Sprintf(`{"hosts": [%s]}`, hosts)
	if count > 0 {
		placement = fmt.Sprintf(`{"hosts": [%s], "count": %d}`, hosts, count)
	}
	return fmt.Sprintf(`{"service_type": "smb", "service_id": "dev", "cluster_id": "dev", "placement": %s}`, placement)
}

// --- ResolveSMBPlacement ---

func (s *smbSuite) resolve(placementJSON string, members ...string) ([]string, error) {
	var spec types.SMBSpec
	err := json.Unmarshal([]byte(fmt.Sprintf(`{"cluster_id": "dev", "placement": %s}`, placementJSON)), &spec)
	assert.NoError(s.T(), err)
	return ResolveSMBPlacement(&spec, members)
}

func (s *smbSuite) TestResolveHosts() {
	nodes, err := s.resolve(`{"hosts": ["m2", "m1"]}`, "m1", "m2", "m3")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2"}, nodes)
}

func (s *smbSuite) TestResolveHostsUnknown() {
	_, err := s.resolve(`{"hosts": ["m1", "ghost"]}`, "m1", "m2")
	assert.ErrorContains(s.T(), err, "ghost")
}

func (s *smbSuite) TestResolveHostsDeduped() {
	nodes, err := s.resolve(`{"hosts": ["m1", "m1", "m2"]}`, "m1", "m2")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2"}, nodes)
}

func (s *smbSuite) TestResolveCount() {
	nodes, err := s.resolve(`{"count": 2}`, "m3", "m1", "m2")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2"}, nodes)
}

func (s *smbSuite) TestResolveCountTooLarge() {
	_, err := s.resolve(`{"count": 4}`, "m1", "m2", "m3")
	assert.ErrorContains(s.T(), err, "count")
}

func (s *smbSuite) TestResolveHostsWithCount() {
	nodes, err := s.resolve(`{"hosts": ["m3", "m1", "m2"], "count": 2}`, "m1", "m2", "m3")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2"}, nodes)
}

func (s *smbSuite) TestResolveLabelUnsupported() {
	_, err := s.resolve(`{"label": "smb"}`, "m1")
	assert.ErrorContains(s.T(), err, "label")
}

func (s *smbSuite) TestResolveCountPerHostUnsupported() {
	_, err := s.resolve(`{"hosts": ["m1"], "count_per_host": 2}`, "m1")
	assert.ErrorContains(s.T(), err, "count_per_host")
}

func (s *smbSuite) TestResolveHostPatternUnsupported() {
	_, err := s.resolve(`{"host_pattern": "m*"}`, "m1")
	assert.ErrorContains(s.T(), err, "host_pattern")
}

func (s *smbSuite) TestResolveEmptyPlacement() {
	_, err := s.resolve(`{}`, "m1")
	assert.ErrorContains(s.T(), err, "placement")
}

// --- DiffSMBPlacement ---

func (s *smbSuite) TestDiffIdempotent() {
	toEnable, toDisable := DiffSMBPlacement([]string{"m1", "m2"}, []string{"m1", "m2"})
	assert.Empty(s.T(), toEnable)
	assert.Empty(s.T(), toDisable)
}

func (s *smbSuite) TestDiffFresh() {
	toEnable, toDisable := DiffSMBPlacement([]string{"m1", "m2"}, nil)
	assert.Equal(s.T(), []string{"m1", "m2"}, toEnable)
	assert.Empty(s.T(), toDisable)
}

func (s *smbSuite) TestDiffMemberChange() {
	toEnable, toDisable := DiffSMBPlacement([]string{"m1", "m2"}, []string{"m2", "m3"})
	assert.Equal(s.T(), []string{"m1"}, toEnable)
	assert.Equal(s.T(), []string{"m3"}, toDisable)
}

// --- ApplySMB ---

func (s *smbSuite) TestApplyFresh() {
	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "dev").Return([]string{}, nil).Once()

	err := ApplySMB(context.Background(), s.TestStateInterface, smbPayload(`"m1", "m2", "m3"`, 0))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2", "m3"}, s.enabled)
	assert.Empty(s.T(), s.disabled)
	// A fresh apply regenerates every member so all nodes files carry the
	// complete membership (early joiners rendered before later rows).
	assert.Equal(s.T(), []string{"m1", "m2", "m3"}, s.regenerated)
}

func (s *smbSuite) TestApplyIdempotent() {
	payload := smbPayload(`"m1", "m2", "m3"`, 0)
	canonical := mustCompactJSON(payload)

	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "dev").Return([]string{"m1", "m2", "m3"}, nil).Once()
	db.On("GetGroupConfig", context.Background(), s.TestStateInterface, "smb", "dev").Return(canonical, nil).Once()

	err := ApplySMB(context.Background(), s.TestStateInterface, payload)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), s.enabled)
	assert.Empty(s.T(), s.disabled)
	assert.Empty(s.T(), s.regenerated)
}

func (s *smbSuite) TestApplyMemberChange() {
	payload := smbPayload(`"m1", "m2"`, 0)
	canonical := mustCompactJSON(payload)

	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "dev").Return([]string{"m2", "m3"}, nil).Once()
	db.On("GetGroupConfig", context.Background(), s.TestStateInterface, "smb", "dev").Return(canonical, nil).Once()

	err := ApplySMB(context.Background(), s.TestStateInterface, payload)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1"}, s.enabled)
	assert.Equal(s.T(), []string{"m3"}, s.disabled)
	assert.Equal(s.T(), []string{"m1", "m2"}, s.regenerated)
}

func (s *smbSuite) TestApplyConfigChange() {
	payload := smbPayload(`"m1", "m2"`, 0)
	canonical := mustCompactJSON(payload)

	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "dev").Return([]string{"m1", "m2"}, nil).Once()
	db.On("GetGroupConfig", context.Background(), s.TestStateInterface, "smb", "dev").Return(`{"stale": true}`, nil).Once()
	db.On("UpdateGroupConfig", context.Background(), s.TestStateInterface, "smb", "dev", canonical).Return(nil).Once()

	err := ApplySMB(context.Background(), s.TestStateInterface, payload)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), s.enabled)
	assert.Empty(s.T(), s.disabled)
	// Spec content changed with steady membership: every member re-renders.
	assert.Equal(s.T(), []string{"m1", "m2"}, s.regenerated)
}

func (s *smbSuite) TestApplyInvalidSpec() {
	err := ApplySMB(context.Background(), s.TestStateInterface, `{"cluster_id": "-bad-"}`)
	assert.ErrorIs(s.T(), err, ErrInvalidSMBSpec)
	assert.Empty(s.T(), s.enabled)
	assert.Empty(s.T(), s.disabled)
}

func (s *smbSuite) TestApplyUnknownHost() {
	err := ApplySMB(context.Background(), s.TestStateInterface, smbPayload(`"m1", "ghost"`, 0))
	assert.ErrorIs(s.T(), err, ErrInvalidSMBSpec)
	assert.Empty(s.T(), s.enabled)
}

// --- RemoveSMB ---

func (s *smbSuite) TestRemoveSMB() {
	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "dev").Return([]string{"m1", "m2"}, nil).Once()

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "auth", "del", "client.smb.dev").Return("", nil).Once()
	common.ProcessExec = r

	err := RemoveSMB(context.Background(), s.TestStateInterface, "dev")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"m1", "m2"}, s.disabled)
}

func (s *smbSuite) TestRemoveSMBUnknown() {
	db := s.withDB()
	db.On("GetGroupMembers", context.Background(), s.TestStateInterface, "smb", "ghost").Return([]string{}, nil).Once()

	err := RemoveSMB(context.Background(), s.TestStateInterface, "ghost")
	assert.ErrorContains(s.T(), err, "no smb cluster")
	assert.Empty(s.T(), s.disabled)
}

// --- ListSMB ---

func (s *smbSuite) TestListSMB() {
	db := s.withDB()
	db.On("GetGroupedServices", context.Background(), s.TestStateInterface).Return([]database.GroupedService{
		{Service: "smb", GroupID: "dev", Member: "m2"},
		{Service: "smb", GroupID: "dev", Member: "m1"},
		{Service: "nfs", GroupID: "other", Member: "m1"},
	}, nil).Once()
	db.On("GetGroupConfig", context.Background(), s.TestStateInterface, "smb", "dev").Return(`{"cluster_id":"dev"}`, nil).Once()

	statuses, err := ListSMB(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
	assert.Len(s.T(), statuses, 1)
	assert.Equal(s.T(), "dev", statuses[0].ClusterID)
	assert.Equal(s.T(), []string{"m1", "m2"}, statuses[0].PlacedOn)
	assert.JSONEq(s.T(), `{"cluster_id":"dev"}`, string(statuses[0].Spec))
}
