package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"
)

// sampleSMBSpec is the reference SMBSpec JSON emitted by mgr/smb
// (ceph service_spec.py serialization).
const sampleSMBSpec = `{
  "service_type": "smb",
  "service_id": "dev",
  "placement": {"hosts": ["smbdev-1", "smbdev-2", "smbdev-3"]},
  "cluster_id": "dev",
  "features": [],
  "config_uri": "rados://.smb/dev/scc.dev.json",
  "user_sources": ["rados://.smb/dev/users.dev.json"],
  "cluster_meta_uri": "rados://.smb/dev/cluster.meta.json",
  "cluster_lock_uri": "rados://.smb/dev/cluster.meta.lock",
  "cluster_public_addrs": [
    {"address": "10.105.154.245/24", "destination": null}
  ]
}`

type SMBSpecSuite struct {
	suite.Suite
}

func TestSMBSpecSuite(t *testing.T) {
	suite.Run(t, new(SMBSpecSuite))
}

func (s *SMBSpecSuite) TestUnmarshalSampleSpec() {
	var spec SMBSpec
	err := json.Unmarshal([]byte(sampleSMBSpec), &spec)
	s.NoError(err)

	s.Equal("smb", spec.ServiceType)
	s.Equal("dev", spec.ServiceID)
	s.Equal("dev", spec.ClusterID)
	s.Equal([]string{"smbdev-1", "smbdev-2", "smbdev-3"}, spec.Placement.Hosts)
	s.Empty(spec.Features)
	s.Equal("rados://.smb/dev/scc.dev.json", spec.ConfigURI)
	s.Equal([]string{"rados://.smb/dev/users.dev.json"}, spec.UserSources)
	s.Equal("rados://.smb/dev/cluster.meta.json", spec.ClusterMetaURI)
	s.Equal("rados://.smb/dev/cluster.meta.lock", spec.ClusterLockURI)
	s.Require().Len(spec.ClusterPublicAddrs, 1)
	s.Equal("10.105.154.245/24", spec.ClusterPublicAddrs[0].Address)
	s.Empty(spec.ClusterPublicAddrs[0].Destination)
}

func (s *SMBSpecSuite) TestUnmarshalPlacementCountAndLabel() {
	var spec SMBSpec
	err := json.Unmarshal([]byte(`{"placement": {"count": 3, "label": "smb"}}`), &spec)
	s.NoError(err)
	s.Equal(3, spec.Placement.Count)
	s.Equal("smb", spec.Placement.Label)
}

func (s *SMBSpecSuite) TestUnmarshalDestinationString() {
	var addr SMBPublicAddrSpec
	err := json.Unmarshal([]byte(`{"address": "10.0.0.1/24", "destination": "10.0.0.0/24"}`), &addr)
	s.NoError(err)
	s.Equal(SMBDestination{"10.0.0.0/24"}, addr.Destination)
}

func (s *SMBSpecSuite) TestUnmarshalDestinationList() {
	var addr SMBPublicAddrSpec
	err := json.Unmarshal([]byte(`{"address": "10.0.0.1/24", "destination": ["10.0.0.0/24", "10.1.0.0/24"]}`), &addr)
	s.NoError(err)
	s.Equal(SMBDestination{"10.0.0.0/24", "10.1.0.0/24"}, addr.Destination)
}

func (s *SMBSpecSuite) TestUnmarshalDestinationInvalid() {
	var addr SMBPublicAddrSpec
	err := json.Unmarshal([]byte(`{"address": "10.0.0.1/24", "destination": 42}`), &addr)
	s.Error(err)
}

func (s *SMBSpecSuite) TestUnknownFieldsTolerated() {
	var spec SMBSpec
	err := json.Unmarshal([]byte(`{"cluster_id": "dev", "some_future_field": {"x": 1}}`), &spec)
	s.NoError(err)
	s.Equal("dev", spec.ClusterID)
}
