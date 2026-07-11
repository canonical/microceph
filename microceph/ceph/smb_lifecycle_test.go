package ceph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type smbLifecycleSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestSMBLifecycleSuite(t *testing.T) {
	suite.Run(t, new(smbLifecycleSuite))
}

func (s *smbLifecycleSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.TestStateInterface = mocks.NewStateInterface(s.T())

	originalAddresses := smbMemberAddressesFunc
	originalIface := resolveSMBIfaceFunc
	s.T().Cleanup(func() {
		smbMemberAddressesFunc = originalAddresses
		resolveSMBIfaceFunc = originalIface
	})

	smbMemberAddressesFunc = func(st interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"host1": "10.0.0.1", "host2": "10.0.0.2"}, nil
	}
	resolveSMBIfaceFunc = func(cidr string) (string, error) { return "enp5s0", nil }
}

// lifecycleEnv builds a fake snap tree (stock ctdb files) and render
// params rooted in temp dirs.
func (s *smbLifecycleSuite) lifecycleEnv() SMBRenderParams {
	root := s.T().TempDir()
	snapDir := filepath.Join(root, "snap")

	for _, dir := range []string{
		filepath.Join(snapDir, "etc", "ctdb", "events", "legacy"),
		filepath.Join(snapDir, "ctdb", "events", "legacy"),
	} {
		assert.NoError(s.T(), os.MkdirAll(dir, 0755))
	}
	for _, script := range smbStockCTDBScripts {
		assert.NoError(s.T(), os.WriteFile(filepath.Join(snapDir, "etc", "ctdb", "events", "legacy", script), []byte("#!/bin/sh\n"), 0755))
	}
	assert.NoError(s.T(), os.WriteFile(filepath.Join(snapDir, "etc", "ctdb", "functions"), []byte("# functions\n"), 0644))
	assert.NoError(s.T(), os.WriteFile(filepath.Join(snapDir, "ctdb", "events", "legacy", "50.samba.script"), []byte("#!/bin/sh\n"), 0755))
	assert.NoError(s.T(), os.WriteFile(filepath.Join(snapDir, "ctdb", "script.options"), []byte("CTDB_SAMBA_SKIP_SHARE_CHECK=yes\n"), 0644))

	return SMBRenderParams{
		ClusterID: "dev",
		Hostname:  "host1",
		Entity:    "client.smb.dev.host1",
		Clustered: true,
		Paths: SMBPaths{
			Conf: filepath.Join(root, "conf"),
			Run:  filepath.Join(root, "run"),
			Data: filepath.Join(root, "data"),
			Log:  filepath.Join(root, "logs"),
			Snap: snapDir,
		},
	}
}

func (s *smbLifecycleSuite) withDB() *mocks.GroupedServiceQueryIntf {
	db := mocks.NewGroupedServiceQueryIntf(s.T())
	originalDB := database.GroupedServicesQuery
	s.T().Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db
	return db
}

func (s *smbLifecycleSuite) memberRecords() []database.GroupedService {
	return []database.GroupedService{
		{ID: 1, Service: "smb", GroupID: "dev", Member: "host1"},
		{ID: 2, Service: "smb", GroupID: "dev", Member: "host2"},
	}
}

func (s *smbLifecycleSuite) TestEnableSMBNodeLocal() {
	p := s.lifecycleEnv()
	ctx := context.Background()

	var spec types.SMBSpec
	assert.NoError(s.T(), json.Unmarshal([]byte(validSMBPayload), &spec))

	configJSON, err := os.ReadFile(filepath.Join("testdata", "smb", "config.smb.json"))
	assert.NoError(s.T(), err)

	db := s.withDB()
	db.On("GetGroupMemberRecords", ctx, s.TestStateInterface, "smb", "dev").Return(s.memberRecords(), nil).Once()

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "auth", "get-or-create", "client.smb.dev.host1").Return("", nil).Once()
	r.On("RunCommand", "ceph", "auth", "caps", "client.smb.dev.host1",
		"mon", smbMonCaps("dev"), "osd", smbOSDCaps(&spec)).Return("", nil).Once()
	r.On("RunCommand", "ceph", "auth", "get", "client.smb.dev.host1", "-o", mock.Anything).Run(func(args mock.Arguments) {
		assert.NoError(s.T(), os.WriteFile(args.Get(5).(string), []byte("[client]\nkey=x\n"), 0600))
	}).Return("", nil).Once()
	r.On("RunCommand", "rados", "get", "--pool", ".smb", "-N", "dev", "scc.dev.json", "-").
		Return(string(configJSON), nil).Once()
	r.On("RunCommand", "python3", "-m", "sambacc.commands.main",
		"--config", filepath.Join(p.Paths.Conf, "samba", "config.json"),
		"--identity", "dev", "print-config").Return("[global]\nrendered\n", nil).Once()
	r.On("RunCommand", "snapctl", "start", "microceph.ctdbd", "--enable").Return("", nil).Once()
	common.ProcessExec = r

	err = enableSMBNodeLocal(ctx, s.TestStateInterface, &spec, p)
	assert.NoError(s.T(), err)

	// Directories.
	info, err := os.Stat(filepath.Join(p.Paths.Data, "samba", "dev", "private"))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), os.FileMode(0700), info.Mode().Perm())

	// Rendered configs.
	smbConf, err := os.ReadFile(filepath.Join(p.Paths.Conf, "samba", "smb.conf"))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "[global]\nrendered\n", string(smbConf))

	nodes, err := os.ReadFile(filepath.Join(p.Paths.Conf, "ctdb", "nodes"))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "10.0.0.1\n10.0.0.2\n", string(nodes))

	// CTDB_BASE population.
	for _, script := range append(smbStockCTDBScripts, "50.samba.script") {
		link := filepath.Join(p.Paths.Conf, "ctdb", "events", "legacy", script)
		target, err := os.Readlink(link)
		assert.NoError(s.T(), err, script)
		assert.FileExists(s.T(), target, script)
	}
	options, err := os.ReadFile(filepath.Join(p.Paths.Conf, "ctdb", "script.options"))
	assert.NoError(s.T(), err)
	assert.Contains(s.T(), string(options), "CTDB_SAMBA_SKIP_SHARE_CHECK=yes")
}

func (s *smbLifecycleSuite) TestDisableSMBLocal() {
	p := s.lifecycleEnv()
	ctx := context.Background()

	// Seed on-disk state to tear down.
	for _, dir := range []string{
		filepath.Join(p.Paths.Conf, "samba"),
		filepath.Join(p.Paths.Conf, "ctdb"),
		filepath.Join(p.Paths.Data, "samba", "dev"),
		filepath.Join(p.Paths.Run, "samba", "dev"),
	} {
		assert.NoError(s.T(), os.MkdirAll(dir, 0755))
	}
	assert.NoError(s.T(), os.WriteFile(filepath.Join(p.Paths.Conf, "samba", "smb.conf"), []byte("x"), 0644))
	assert.NoError(s.T(), os.WriteFile(filepath.Join(p.Paths.Conf, "samba", "config.json"), []byte("x"), 0644))
	assert.NoError(s.T(), os.WriteFile(filepath.Join(p.Paths.Conf, "ceph.client.smb.dev.host1.keyring"), []byte("k"), 0600))

	canonical := mustCompactJSON(validSMBPayload)

	db := s.withDB()
	db.On("GetGroupConfig", ctx, s.TestStateInterface, "smb", "dev").Return(canonical, nil).Once()
	db.On("RemoveForHost", ctx, s.TestStateInterface, "smb", "dev").Return(nil).Once()

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "stop", "microceph.ctdbd", "--disable").Return("", nil).Once()
	r.On("RunCommand", "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Once()
	r.On("RunCommand", "ceph", "auth", "del", "client.smb.dev.host1").Return("", nil).Once()
	common.ProcessExec = r

	err := disableSMBLocal(ctx, s.TestStateInterface, "dev", p)
	assert.NoError(s.T(), err)

	assert.NoFileExists(s.T(), filepath.Join(p.Paths.Conf, "samba", "smb.conf"))
	assert.NoFileExists(s.T(), filepath.Join(p.Paths.Conf, "ceph.client.smb.dev.host1.keyring"))
	assert.NoDirExists(s.T(), filepath.Join(p.Paths.Conf, "ctdb"))
	assert.NoDirExists(s.T(), filepath.Join(p.Paths.Data, "samba", "dev"))
}

func (s *smbLifecycleSuite) TestRegenerateSMBNodeLocal() {
	p := s.lifecycleEnv()
	ctx := context.Background()

	configJSON, err := os.ReadFile(filepath.Join("testdata", "smb", "config.smb.json"))
	assert.NoError(s.T(), err)

	canonical := mustCompactJSON(validSMBPayload)

	db := s.withDB()
	db.On("GetGroupConfig", ctx, s.TestStateInterface, "smb", "dev").Return(canonical, nil).Once()
	db.On("GetGroupMemberRecords", ctx, s.TestStateInterface, "smb", "dev").Return(s.memberRecords(), nil).Once()

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "rados", "get", "--pool", ".smb", "-N", "dev", "scc.dev.json", "-").
		Return(string(configJSON), nil).Once()
	r.On("RunCommand", "python3", "-m", "sambacc.commands.main",
		"--config", filepath.Join(p.Paths.Conf, "samba", "config.json"),
		"--identity", "dev", "print-config").Return("[global]\nregen\n", nil).Once()
	r.On("RunCommand", "snapctl", "restart", "microceph.ctdbd").Return("", nil).Once()
	common.ProcessExec = r

	err = regenerateSMBNodeLocal(ctx, s.TestStateInterface, "dev", p)
	assert.NoError(s.T(), err)

	smbConf, err := os.ReadFile(filepath.Join(p.Paths.Conf, "samba", "smb.conf"))
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "[global]\nregen\n", string(smbConf))
}

func (s *smbLifecycleSuite) TestOrderedNodeIPsFollowsRowIDs() {
	ctx := context.Background()

	db := s.withDB()
	// Rows deliberately out of row-id order from the mapper.
	db.On("GetGroupMemberRecords", ctx, s.TestStateInterface, "smb", "dev").Return([]database.GroupedService{
		{ID: 2, Member: "host2"},
		{ID: 1, Member: "host1"},
	}, nil).Once()

	ips, err := smbOrderedNodeIPs(ctx, s.TestStateInterface, "dev", "host1")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"10.0.0.1", "10.0.0.2"}, ips)
}

func (s *smbLifecycleSuite) TestOrderedNodeIPsIncludesUnrecordedSelf() {
	ctx := context.Background()

	// During enable, rendering happens before DbUpdate records this node:
	// the local node must still appear in its own nodes file.
	db := s.withDB()
	db.On("GetGroupMemberRecords", ctx, s.TestStateInterface, "smb", "dev").Return([]database.GroupedService{
		{ID: 1, Member: "host1"},
	}, nil).Once()

	ips, err := smbOrderedNodeIPs(ctx, s.TestStateInterface, "dev", "host2")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"10.0.0.1", "10.0.0.2"}, ips)
}
