package ceph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// Golden files live in testdata/smb. Regenerate by running the tests with
// UPDATE_GOLDEN=1 and reviewing the diff.
func (s *smbConfigSuite) golden(name string, got []byte) {
	path := filepath.Join("testdata", "smb", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		assert.NoError(s.T(), os.WriteFile(path, got, 0644))
		return
	}

	want, err := os.ReadFile(path)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), string(want), string(got), name)
}

type smbConfigSuite struct {
	tests.BaseSuite
}

func TestSMBConfigSuite(t *testing.T) {
	suite.Run(t, new(smbConfigSuite))
}

func testRenderParams() SMBRenderParams {
	return SMBRenderParams{
		ClusterID: "dev",
		Entity:    "client.smb.dev.host1",
		Clustered: true,
		Paths: SMBPaths{
			Conf: "/var/snap/microceph/current/conf",
			Run:  "/var/snap/microceph/current/run",
			Data: "/var/snap/microceph/common/data",
			Log:  "/var/snap/microceph/common/logs",
			Snap: "/snap/microceph/current",
		},
	}
}

func (s *smbConfigSuite) TestParseRADOSURI() {
	pool, ns, object, err := parseRADOSURI("rados://.smb/dev/cluster.meta.lock")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), ".smb", pool)
	assert.Equal(s.T(), "dev", ns)
	assert.Equal(s.T(), "cluster.meta.lock", object)

	pool, ns, object, err = parseRADOSURI("rados://pool/obj.json")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "pool", pool)
	assert.Empty(s.T(), ns)
	assert.Equal(s.T(), "obj.json", object)

	_, _, _, err = parseRADOSURI("http://x/y")
	assert.Error(s.T(), err)

	_, _, _, err = parseRADOSURI("rados://poolonly")
	assert.Error(s.T(), err)
}

func (s *smbConfigSuite) TestTranslateSMBConfig() {
	raw, err := os.ReadFile(filepath.Join("testdata", "smb", "config.smb.json"))
	assert.NoError(s.T(), err)

	translated, clustered, err := TranslateSMBConfig(raw, testRenderParams())
	assert.NoError(s.T(), err)
	assert.True(s.T(), clustered)

	s.golden("translated.json.golden", translated)
}

func (s *smbConfigSuite) TestTranslateSMBConfigRejectsWrongVersion() {
	_, _, err := TranslateSMBConfig([]byte(`{"samba-container-config": "v9"}`), testRenderParams())
	assert.ErrorContains(s.T(), err, "samba-container-config")
}

func (s *smbConfigSuite) TestTranslateSMBConfigRejectsMissingIdentity() {
	_, _, err := TranslateSMBConfig([]byte(`{"samba-container-config": "v0", "configs": {"other": {}}}`), testRenderParams())
	assert.ErrorContains(s.T(), err, "dev")
}

func (s *smbConfigSuite) TestRenderCTDBConf() {
	got, err := RenderCTDBConf(testRenderParams(), "rados://.smb/dev/cluster.meta.lock")
	assert.NoError(s.T(), err)
	s.golden("ctdb.conf.golden", []byte(got))
}

func (s *smbConfigSuite) TestRenderCTDBNodes() {
	got := RenderCTDBNodes([]string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})
	s.golden("nodes.golden", []byte(got))
}

func (s *smbConfigSuite) TestRenderCTDBPublicAddresses() {
	addrs := []types.SMBPublicAddrSpec{
		{Address: "10.105.154.245/24"},
		{Address: "10.105.155.1/24", Destination: types.SMBDestination{"10.105.155.0/24"}},
	}

	resolver := func(cidr string) (string, error) { return "enp5s0", nil }

	got, err := RenderCTDBPublicAddresses(addrs, resolver)
	assert.NoError(s.T(), err)
	s.golden("public_addresses.golden", []byte(got))
}

func (s *smbConfigSuite) TestRenderCTDBPublicAddressesResolverError() {
	addrs := []types.SMBPublicAddrSpec{{Address: "10.0.0.1/24"}}
	resolver := func(cidr string) (string, error) { return "", assert.AnError }

	_, err := RenderCTDBPublicAddresses(addrs, resolver)
	assert.Error(s.T(), err)
}

func (s *smbConfigSuite) TestWriteSMBNodeConfigs() {
	confDir := s.T().TempDir()
	p := testRenderParams()
	p.Paths.Conf = confDir

	raw, err := os.ReadFile(filepath.Join("testdata", "smb", "config.smb.json"))
	assert.NoError(s.T(), err)

	var spec types.SMBSpec
	assert.NoError(s.T(), json.Unmarshal([]byte(validSMBPayload), &spec))

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "rados", "get", "--pool", ".smb", "-N", "dev", "scc.dev.json", "-").
		Return(string(raw), nil).Once()
	r.On("RunCommand", "python3", "-m", "sambacc.commands.main",
		"--config", filepath.Join(confDir, "samba", "config.json"),
		"--identity", "dev", "print-config").
		Return("[global]\n\tfake = conf\n", nil).Once()
	common.ProcessExec = r

	resolver := func(cidr string) (string, error) { return "enp5s0", nil }
	err = WriteSMBNodeConfigs(&spec, p, []string{"10.0.0.1", "10.0.0.2"}, resolver)
	assert.NoError(s.T(), err)

	for _, f := range []struct {
		path string
		want string
	}{
		{"samba/smb.conf", "[global]\n\tfake = conf\n"},
		{"ctdb/nodes", "10.0.0.1\n10.0.0.2\n"},
		{"ctdb/public_addresses", "10.105.154.245/24 enp5s0\n"},
	} {
		got, err := os.ReadFile(filepath.Join(confDir, f.path))
		assert.NoError(s.T(), err, f.path)
		assert.Equal(s.T(), f.want, string(got), f.path)

		info, err := os.Stat(filepath.Join(confDir, f.path))
		assert.NoError(s.T(), err)
		assert.Equal(s.T(), os.FileMode(0644), info.Mode().Perm(), f.path)
	}

	ctdbConf, err := os.ReadFile(filepath.Join(confDir, "ctdb", "ctdb.conf"))
	assert.NoError(s.T(), err)
	assert.Contains(s.T(), string(ctdbConf), "cluster lock = !")
	assert.Contains(s.T(), string(ctdbConf), "microceph.reclock.dev")

	// No stray .tmp files left behind.
	for _, dir := range []string{"samba", "ctdb"} {
		entries, err := os.ReadDir(filepath.Join(confDir, dir))
		assert.NoError(s.T(), err)
		for _, entry := range entries {
			assert.NotContains(s.T(), entry.Name(), ".tmp")
		}
	}
}
