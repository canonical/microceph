package ceph

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Rican7/retry/strategy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

// peersStillRegistered is what rbd prints when a pool disable is refused with EBUSY.
const peersStillRegistered = "exit status 16 (2026-09-07T02:40:27.480+0000 7f3c5a7fc6c0 -1 librbd::api::Mirror: mode_set: mirror peers still registered)"

type RbdMirrorSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface

	localDisableRetryStrategies  []strategy.Strategy
	remoteDisableRetryStrategies []strategy.Strategy
}

func TestRbdMirror(t *testing.T) {
	suite.Run(t, new(RbdMirrorSuite))
}

func (ks *RbdMirrorSuite) SetupTest() {
	ks.BaseSuite.SetupTest()
	ks.CopyCephConfigs()

	// Retry without sleeping.
	ks.localDisableRetryStrategies = localDisableRetryStrategies
	ks.remoteDisableRetryStrategies = remoteDisableRetryStrategies
	localDisableRetryStrategies = []strategy.Strategy{strategy.Limit(4)}
	remoteDisableRetryStrategies = []strategy.Strategy{strategy.Limit(10)}
}

func (ks *RbdMirrorSuite) TearDownTest() {
	localDisableRetryStrategies = ks.localDisableRetryStrategies
	remoteDisableRetryStrategies = ks.remoteDisableRetryStrategies
	ks.BaseSuite.TearDownTest()
}

// expectRemovePeer sets the expectations for a RemovePeer call that finds one peer on
// each site for pool "pool", local site "magical" and remote site "simple".
func expectRemovePeer(r *mocks.Runner) {
	localInfo, _ := os.ReadFile("./test_assets/rbd_mirror_pool_info.json")
	remoteInfo := `{"mode":"pool","site_name":"simple","peers":[{"uuid":"remote-uuid","direction":"rx-tx","site_name":"magical"}]}`

	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(string(localInfo), nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json", "--cluster", "simple", "--id", "magical"}...).Return(remoteInfo, nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "f3ee5939-66a6-494f-849a-a4402ddb4d18"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "remote-uuid", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
}

func (ks *RbdMirrorSuite) TestVerbosePoolStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_verbose_pool_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--verbose", "--format", "json"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	// Method call
	resp, err := GetRbdMirrorVerbosePoolStatus("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Name, "pool")
}

func (ks *RbdMirrorSuite) TestPoolStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_pool_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--format", "json"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	// Method call
	resp, err := GetRbdMirrorPoolStatus("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Health, RbdReplicationHealth("OK"))
	assert.Equal(ks.T(), resp.DaemonHealth, RbdReplicationHealth("OK"))
	assert.Equal(ks.T(), resp.ImageHealth, RbdReplicationHealth("OK"))
}

func (ks *RbdMirrorSuite) TestImageStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_image_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "status", "pool/image_one", "--format", "json"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	// Method call
	resp, err := GetRbdMirrorImageStatus("pool", "image_one", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Name, "image_one")
	assert.Equal(ks.T(), resp.IsPrimary, true)
}

func (ks *RbdMirrorSuite) TestPoolInfo() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_pool_info.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	// Method call
	resp, err := GetRbdMirrorPoolInfo("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Mode, types.RbdResourcePool)
	assert.Equal(ks.T(), resp.LocalSiteName, "magical")
	assert.Equal(ks.T(), resp.Peers[0].RemoteName, "simple")
}
func (ks *RbdMirrorSuite) TestPromotePoolOnSecondary() {
	r := mocks.NewRunner(ks.T())
	output, _ := os.ReadFile("./test_assets/rbd_mirror_promote_secondary_failure.txt")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "promote", "pool"}...).Return("", fmt.Errorf("%s", string(output))).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "promote", "pool", "--force"}...).Return("ok", nil).Once()
	common.ProcessExec = r

	// Test stardard promotion.
	err := handlePoolPromotion("pool", false)
	assert.ErrorContains(ks.T(), err, "If you understand the *RISK* and you're *ABSOLUTELY CERTAIN*")

	err = handlePoolPromotion("pool", true)
	assert.NoError(ks.T(), err)
}

func (ks *RbdMirrorSuite) TestDemotePoolOnSecondary() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_verbose_pool_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "demote", "pool"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--verbose", "--format", "json"}...).Return(string(output), nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "resync", "pool/image_one"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "resync", "pool/image_two"}...).Return("ok", nil).Once()
	common.ProcessExec = r

	// Test stardard promotion.
	err := handlePoolDemotion("pool")
	assert.NoError(ks.T(), err)
}

// TestRemovePeerRemovesEveryMatchingPeer checks that all peers named after the remote
// site are removed, and that peers of other sites are left alone.
func (ks *RbdMirrorSuite) TestRemovePeerRemovesEveryMatchingPeer() {
	r := mocks.NewRunner(ks.T())

	localInfo, _ := os.ReadFile("./test_assets/rbd_mirror_pool_info_reregistered_peer.json")
	remoteInfo := `{"mode":"pool","site_name":"simple","peers":[{"uuid":"remote-uuid","direction":"rx-tx","site_name":"magical"}]}`

	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(string(localInfo), nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json", "--cluster", "simple", "--id", "magical"}...).Return(remoteInfo, nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "f3ee5939-66a6-494f-849a-a4402ddb4d18"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "0b0d3f3c-5a53-4b4b-9d0e-6a4a3f0c2f11"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "remote-uuid", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	common.ProcessExec = r

	err := RemovePeer("pool", "magical", "simple")
	assert.NoError(ks.T(), err)
}

// TestRemovePeerNoPeer checks the error when no peer is named after the remote site.
func (ks *RbdMirrorSuite) TestRemovePeerNoPeer() {
	r := mocks.NewRunner(ks.T())

	localInfo, _ := os.ReadFile("./test_assets/rbd_mirror_pool_info.json")

	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(string(localInfo), nil).Once()
	common.ProcessExec = r

	err := RemovePeer("pool", "magical", "absent")
	assert.ErrorIs(ks.T(), err, errNoPeerFound)
	assert.EqualError(ks.T(), err, "no peer found")
}

// TestDisablePoolMirroringRetriesLocalDisable checks that a local pool disable refused
// because a peer was re-registered is retried after removing that peer.
func (ks *RbdMirrorSuite) TestDisablePoolMirroringRetriesLocalDisable() {
	r := mocks.NewRunner(ks.T())

	reregistered := `{"mode":"pool","site_name":"magical","peers":[{"uuid":"ghost-uuid","direction":"tx-only","site_name":"simple"}]}`

	expectRemovePeer(r)
	// first local disable is refused, the re-registered peer is removed, second succeeds.
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", fmt.Errorf("%s", peersStillRegistered)).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(reregistered, nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "ghost-uuid"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	common.ProcessExec = r

	err := DisablePoolMirroring("pool", RbdReplicationPeer{}, "magical", "simple")
	assert.NoError(ks.T(), err)
}

// TestDisablePoolMirroringRetriesRemoteDisable checks that a remote pool disable refused
// because a peer was re-registered is retried after removing that peer on the remote.
func (ks *RbdMirrorSuite) TestDisablePoolMirroringRetriesRemoteDisable() {
	r := mocks.NewRunner(ks.T())

	reregistered := `{"mode":"pool","site_name":"simple","peers":[{"uuid":"ghost-uuid","direction":"tx-only","site_name":"magical"}]}`

	expectRemovePeer(r)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", nil).Once()
	// first remote disable is refused, the re-registered peer is removed, second succeeds.
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", fmt.Errorf("%s", peersStillRegistered)).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json", "--cluster", "simple", "--id", "magical"}...).Return(reregistered, nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "remove", "pool", "ghost-uuid", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	common.ProcessExec = r

	err := DisablePoolMirroring("pool", RbdReplicationPeer{}, "magical", "simple")
	assert.NoError(ks.T(), err)
}

// TestDisablePoolMirroringLocalDisableGivesUp checks that the local pool disable is
// bounded, that a retry with no re-registered peer still re-runs the disable, and that
// the remote disable is not attempted after the local one fails.
func (ks *RbdMirrorSuite) TestDisablePoolMirroringLocalDisableGivesUp() {
	r := mocks.NewRunner(ks.T())

	noPeers := `{"mode":"pool","site_name":"magical","peers":[]}`

	expectRemovePeer(r)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", fmt.Errorf("%s", peersStillRegistered)).Times(4)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(noPeers, nil).Times(3)
	common.ProcessExec = r

	err := DisablePoolMirroring("pool", RbdReplicationPeer{}, "magical", "simple")
	assert.ErrorContains(ks.T(), err, "mirror peers still registered")
}

// TestDisablePoolMirroringLocalDisableFailsFastOnUnrelatedError checks that a local
// disable failure that is not the peer-reregistration signature is not retried: the mock
// only tolerates a single "rbd mirror pool disable" call and no peer lookup at all, so a
// second attempt would fail the test on an unexpected call.
func (ks *RbdMirrorSuite) TestDisablePoolMirroringLocalDisableFailsFastOnUnrelatedError() {
	r := mocks.NewRunner(ks.T())

	expectRemovePeer(r)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", fmt.Errorf("rbd: pool magical/pool does not exist")).Once()
	common.ProcessExec = r

	err := DisablePoolMirroring("pool", RbdReplicationPeer{}, "magical", "simple")
	assert.ErrorContains(ks.T(), err, "does not exist")
}

// TestDisablePoolMirroringRemoteDisableRetriesUnrelatedError checks that the remote
// disable loop, unlike the local one, keeps retrying a failure that is not the
// peer-reregistration signature, as it has since
// https://github.com/canonical/microceph/pull/468: the remote disable can run before the
// new state has propagated to the remote cluster. The second attempt looks up
// re-registered peers on the remote first, finds none, removes nothing and succeeds.
func (ks *RbdMirrorSuite) TestDisablePoolMirroringRemoteDisableRetriesUnrelatedError() {
	r := mocks.NewRunner(ks.T())

	noPeers := `{"mode":"pool","site_name":"simple","peers":[]}`

	expectRemovePeer(r)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", nil).Once()
	// first remote disable fails on an unrelated error, the peer lookup finds nothing to
	// remove, second succeeds.
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", fmt.Errorf("rbd: error disabling mirroring for one or more images")).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json", "--cluster", "simple", "--id", "magical"}...).Return(noPeers, nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	common.ProcessExec = r

	err := DisablePoolMirroring("pool", RbdReplicationPeer{}, "magical", "simple")
	assert.NoError(ks.T(), err)
}

// TestRemoteDisableRetryDelaysAreSeconds guards the "strategy.Delay(5) slept 5ns instead
// of 5s" unit bug directly: SetupTest overrides remoteDisableRetryStrategies wholesale
// for every other test in this suite, so only an assertion on the extracted constants
// themselves exercises the real production durations.
func (ks *RbdMirrorSuite) TestRemoteDisableRetryDelaysAreSeconds() {
	assert.Equal(ks.T(), 5*time.Second, remoteDisableInitialDelay)
	assert.Equal(ks.T(), 5*time.Second, remoteDisableBackoffStep)
}
