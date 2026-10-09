package ceph

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Rican7/retry/strategy"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

// RbdReplicationSuite tests how the RBD replication handlers deal with the remote records
// of a site and the peers of a pool. Pool "pool" is mirrored between local site "magical"
// and remote site "simple", the names the rbd mirror test assets and expectRemovePeer use.
type RbdReplicationSuite struct {
	tests.BaseSuite

	getRemoteDb                  func(context.Context, mcTypes.State, string) (types.RemoteRecords, error)
	processExec                  common.Runner
	localDisableRetryStrategies  []strategy.Strategy
	remoteDisableRetryStrategies []strategy.Strategy
}

// TestRbdReplication runs RbdReplicationSuite.
func TestRbdReplication(t *testing.T) {
	suite.Run(t, new(RbdReplicationSuite))
}

// SetupTest keeps what the tests replace and makes the pool disable retry without sleeping.
func (s *RbdReplicationSuite) SetupTest() {
	s.BaseSuite.SetupTest()

	s.getRemoteDb = database.GetRemoteDb
	s.processExec = common.ProcessExec

	// Retry without sleeping.
	s.localDisableRetryStrategies = localDisableRetryStrategies
	s.remoteDisableRetryStrategies = remoteDisableRetryStrategies
	localDisableRetryStrategies = []strategy.Strategy{strategy.Limit(4)}
	remoteDisableRetryStrategies = []strategy.Strategy{strategy.Limit(10)}
}

// TearDownTest puts back what SetupTest kept.
func (s *RbdReplicationSuite) TearDownTest() {
	database.GetRemoteDb = s.getRemoteDb
	common.ProcessExec = s.processExec
	localDisableRetryStrategies = s.localDisableRetryStrategies
	remoteDisableRetryStrategies = s.remoteDisableRetryStrategies

	s.BaseSuite.TearDownTest()
}

// stubRemotes replaces database.GetRemoteDb with a fake that answers like the real
// query: every record for an empty name, the named record otherwise, an error for a
// name that is not there, and no records and no error for an empty table.
func (s *RbdReplicationSuite) stubRemotes(records ...types.RemoteRecord) {
	database.GetRemoteDb = func(ctx context.Context, state mcTypes.State, name string) (types.RemoteRecords, error) {
		var found types.RemoteRecords
		for _, record := range records {
			if name == "" || record.Name == name {
				found = append(found, record)
			}
		}

		if name != "" && len(found) == 0 {
			return nil, fmt.Errorf("failed to add client config: Remote not found")
		}

		return found, nil
	}
}

// noCommands makes any rbd command fail the test, for requests that must be refused
// before anything is run.
func (s *RbdReplicationSuite) noCommands() {
	common.ProcessExec = mocks.NewRunner(s.T())
}

// disable runs the disable handler the way the state machine does and fails the test if
// it panics, which the API turns into a dropped connection.
func (s *RbdReplicationSuite) disable(rh *RbdReplicationHandler) error {
	var err error
	var resp string
	assert.NotPanics(s.T(), func() {
		err = rh.DisableHandler(context.Background(), rh, &resp, interfaces.CephState{})
	})

	return err
}

// enable runs the enable handler the way the state machine does and fails the test if
// it panics.
func (s *RbdReplicationSuite) enable(rh *RbdReplicationHandler) error {
	var err error
	var resp string
	assert.NotPanics(s.T(), func() {
		err = rh.EnableHandler(context.Background(), rh, &resp, interfaces.CephState{})
	})

	return err
}

// poolHandler returns the handler of a request for pool "pool", which is mirrored in the
// given mode with the given peers and is otherwise healthy.
func poolHandler(mode types.RbdResourceType, peers ...RbdReplicationPeer) *RbdReplicationHandler {
	state := StateEnabledReplication
	if mode == types.RbdResourceDisabled {
		state = StateDisabledReplication
	}

	return &RbdReplicationHandler{
		PoolInfo:   RbdReplicationPoolInfo{Mode: mode, LocalSiteName: "magical", Peers: peers},
		PoolStatus: RbdReplicationPoolStatus{State: state, Health: RbdReplicationHealthOK},
		Request: types.RbdReplicationRequest{
			SourcePool:   "pool",
			ResourceType: types.RbdResourcePool,
		},
	}
}

// imageHandler returns the handler of a request for image "image_one" of that pool.
func imageHandler(mode types.RbdResourceType, peers ...RbdReplicationPeer) *RbdReplicationHandler {
	rh := poolHandler(mode, peers...)
	rh.Request.ResourceType = types.RbdResourceImage
	rh.Request.SourceImage = "image_one"
	rh.ImageStatus = RbdReplicationImageStatus{State: StateEnabledReplication}

	return rh
}

// simplePeer is the peer of "pool" for the remote "simple", as in rbd_mirror_pool_info.json.
var simplePeer = RbdReplicationPeer{
	Id:         "f3ee5939-66a6-494f-849a-a4402ddb4d18",
	MirrorId:   "84f58bda-4eea-45b1-9a5a-296cf1b82a65",
	RemoteName: "simple",
	Direction:  types.RbdReplicationDirectionRXTX,
}

// expectNoPoolImages sets the expectation for the lookup of the images that the disable of
// "pool" in pool mirroring mode starts with: the pool has none to disable.
func expectNoPoolImages(r *mocks.Runner) {
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--verbose", "--format", "json"}...).Return(`{"summary":{"health":"OK"},"images":[]}`, nil).Once()
}

// expectPoolDisable sets the expectations for disabling "pool" itself between "magical"
// and "simple": one peer on each site to remove, then the local and the remote pool
// disable.
func expectPoolDisable(r *mocks.Runner) {
	expectRemovePeer(r)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "disable", "pool", "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
}

// expectPoolEnable sets the expectations for enabling "pool" in the given mode between
// "magical" and "simple": the pool is enabled on both sites, then the peer is
// bootstrapped from "magical" to "simple" through a token file, which needs the config
// directories that CopyCephConfigs makes.
func expectPoolEnable(r *mocks.Runner, mode types.RbdResourceType) {
	token := filepath.Join(constants.GetPathConst().ConfPath, "rbd_mirror", "simple_peer_keyring")
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "enable", "pool", string(mode)}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "enable", "pool", string(mode), "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "bootstrap", "create", "--site-name", "magical", "pool"}...).Return("bootstraptoken", nil).Once()
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "peer", "bootstrap", "import", "--site-name", "simple", "--direction", "rx-tx", "pool", token, "--cluster", "simple", "--id", "magical"}...).Return("", nil).Once()
}

// TestDisableHandlerNoRemoteRecords checks that a disable on a site whose remote table
// is empty, as when the remote import never happened, is refused with an error that
// says so. GetRemoteDb returns no rows and no error then, which used to end in an index
// out of range panic and a dropped connection.
func (s *RbdReplicationSuite) TestDisableHandlerNoRemoteRecords() {
	s.stubRemotes()
	s.noCommands()

	err := s.disable(poolHandler(types.RbdResourcePool, simplePeer))
	assert.ErrorIs(s.T(), err, errNoRemoteConfigured)
	assert.ErrorContains(s.T(), err, "no remote configured on this site")

	err = s.disable(imageHandler(types.RbdResourceImage, simplePeer))
	assert.ErrorIs(s.T(), err, errNoRemoteConfigured)
}

// TestDisableThroughStateMachineNoRemoteRecords fires the disable event of a pool that
// has mirroring enabled on a site with no remote record the way the API does. The
// handler runs as the entry action of the transition to the disabled state.
func (s *RbdReplicationSuite) TestDisableThroughStateMachineNoRemoteRecords() {
	s.stubRemotes()
	s.noCommands()

	var err error
	var resp string
	rh := poolHandler(types.RbdResourcePool, simplePeer)
	assert.NotPanics(s.T(), func() {
		err = GetReplicationStateMachine(StateEnabledReplication).FireCtx(
			context.Background(), constants.EventDisableReplication, rh, &resp, interfaces.CephState{},
		)
	})
	assert.ErrorContains(s.T(), err, "no remote configured on this site")
}

// TestEnableHandlerNoRemoteRecords checks the same for enable. The CLI always names the
// remote, so this only happens for an API request that does not.
func (s *RbdReplicationSuite) TestEnableHandlerNoRemoteRecords() {
	s.stubRemotes()
	s.noCommands()

	err := s.enable(poolHandler(types.RbdResourceDisabled))
	assert.ErrorIs(s.T(), err, errNoRemoteConfigured)
}

// TestEnableHandlerUnknownRemote checks that naming a remote that is not configured
// still fails with the error of the lookup.
func (s *RbdReplicationSuite) TestEnableHandlerUnknownRemote() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	s.noCommands()

	rh := poolHandler(types.RbdResourceDisabled)
	rh.Request.RemoteName = "absent"
	err := s.enable(rh)
	assert.ErrorContains(s.T(), err, "remote (absent) does not exist")
}

// TestEnableHandlerPool checks the enable that the CLI sends for a pool: the request names
// its remote, the pool is enabled here and on the remote, this site is called what the
// remote record says, and the peer is bootstrapped from here to there.
func (s *RbdReplicationSuite) TestEnableHandlerPool() {
	s.CopyCephConfigs()
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
	)
	r := mocks.NewRunner(s.T())
	expectPoolEnable(r, types.RbdResourcePool)
	common.ProcessExec = r

	rh := poolHandler(types.RbdResourceDisabled)
	rh.Request.RemoteName = "simple"
	rh.Request.SkipAutoEnable = true
	err := s.enable(rh)
	assert.NoError(s.T(), err)
}

// TestEnableHandlerImage checks the same for an image of a pool that is not mirrored yet:
// the pool is enabled in image mirroring mode, then the image.
func (s *RbdReplicationSuite) TestEnableHandlerImage() {
	s.CopyCephConfigs()
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
	)
	r := mocks.NewRunner(s.T())
	expectPoolEnable(r, types.RbdResourceImage)
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "enable", "pool/image_one", string(types.RbdReplicationJournaling)}...).Return("", nil).Once()
	common.ProcessExec = r

	rh := imageHandler(types.RbdResourceDisabled)
	rh.Request.RemoteName = "simple"
	rh.Request.ReplicationType = types.RbdReplicationJournaling
	err := s.enable(rh)
	assert.NoError(s.T(), err)
}

// TestDisableHandlerOneMatchingRemote checks the usual case: one remote, and it is the
// pool's peer. The pool is disabled against that remote.
func (s *RbdReplicationSuite) TestDisableHandlerOneMatchingRemote() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	r := mocks.NewRunner(s.T())
	expectNoPoolImages(r)
	expectPoolDisable(r)
	common.ProcessExec = r

	err := s.disable(poolHandler(types.RbdResourcePool, simplePeer))
	assert.NoError(s.T(), err)
}

// TestDisableHandlerPeerSelectsRemote checks that with several remotes configured the
// pool's peer decides which one is used, not the first row of the remote table: the
// records are listed by name, so "adecoy" comes first, and the local names that "adecoy"
// and "zeta" carry would show up in the rbd commands if either were picked.
func (s *RbdReplicationSuite) TestDisableHandlerPeerSelectsRemote() {
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
		types.RemoteRecord{ID: 2, Name: "zeta", LocalName: "other"},
	)
	r := mocks.NewRunner(s.T())
	expectNoPoolImages(r)
	expectPoolDisable(r)
	common.ProcessExec = r

	err := s.disable(poolHandler(types.RbdResourcePool, simplePeer))
	assert.NoError(s.T(), err)
}

// TestDisableHandlerNamedRemote checks a request that names its remote, which the API
// allows although the CLI does not.
func (s *RbdReplicationSuite) TestDisableHandlerNamedRemote() {
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
	)
	r := mocks.NewRunner(s.T())
	expectNoPoolImages(r)
	expectPoolDisable(r)
	common.ProcessExec = r

	rh := poolHandler(types.RbdResourcePool, simplePeer)
	rh.Request.RemoteName = "simple"
	err := s.disable(rh)
	assert.NoError(s.T(), err)
}

// TestDisableHandlerNamedRemoteNotAPeer checks that a named remote the pool is not
// mirrored with is refused before any image is touched, without suggesting an import.
func (s *RbdReplicationSuite) TestDisableHandlerNamedRemoteNotAPeer() {
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
	)
	s.noCommands()

	rh := poolHandler(types.RbdResourcePool, simplePeer)
	rh.Request.RemoteName = "adecoy"
	err := s.disable(rh)
	assert.ErrorContains(s.T(), err, `requested remote (adecoy) does not match pool (pool) mirror peer site (simple)`)
	assert.NotContains(s.T(), err.Error(), "import")
}

// TestDisableHandlerNoPeers checks that a pool that has mirroring enabled but no peer is
// refused before any image is touched. The peer used to be indexed unchecked, so this
// panicked.
func (s *RbdReplicationSuite) TestDisableHandlerNoPeers() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	s.noCommands()

	err := s.disable(poolHandler(types.RbdResourcePool))
	assert.ErrorContains(s.T(), err, "no mirror peer registered")
}

// TestDisableHandlerPeerNotARemote checks that a pool mirrored with a site this site has
// no remote for, the case of a missed import next to an unrelated remote, is refused
// with an error that names both.
func (s *RbdReplicationSuite) TestDisableHandlerPeerNotARemote() {
	s.stubRemotes(types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"})
	s.noCommands()

	err := s.disable(poolHandler(types.RbdResourcePool, simplePeer))
	assert.ErrorContains(s.T(), err, "pool (pool) is mirrored with simple")
	assert.ErrorContains(s.T(), err, "configured: adecoy")
}

// TestDisableHandlerPoolAlreadyDisabled checks that disabling a pool that is not
// mirrored stays a no-op that needs no peer, whatever the remote table holds.
func (s *RbdReplicationSuite) TestDisableHandlerPoolAlreadyDisabled() {
	s.stubRemotes(
		types.RemoteRecord{ID: 3, Name: "adecoy", LocalName: "decoy"},
		types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"},
	)
	s.noCommands()

	err := s.disable(poolHandler(types.RbdResourceDisabled))
	assert.NoError(s.T(), err)
}

// TestDisableHandlerImageModeWithImages checks that the check for images that are still
// mirrored comes before the check of the pool's peers: this pool has none, and the error
// must still be about its images.
func (s *RbdReplicationSuite) TestDisableHandlerImageModeWithImages() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	s.noCommands()

	rh := poolHandler(types.RbdResourceImage)
	rh.PoolStatus.ImageCount = 2
	err := s.disable(rh)
	assert.ErrorContains(s.T(), err, "in Image mirroring mode, has 2 images mirroring")
}

// TestDisableHandlerImageModeNoImages checks that a pool in image mirroring mode that
// mirrors no image any more is disabled itself, without the pass over its images that a
// pool in pool mirroring mode needs: in image mode they are not mirrored through the pool.
func (s *RbdReplicationSuite) TestDisableHandlerImageModeNoImages() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	r := mocks.NewRunner(s.T())
	expectPoolDisable(r)
	common.ProcessExec = r

	err := s.disable(poolHandler(types.RbdResourceImage, simplePeer))
	assert.NoError(s.T(), err)
}

// TestDisableHandlerImageNeedsNoPeer checks that disabling one image does not depend on
// the pool's peers: it never used the remote, and must keep working for a pool whose
// peers are gone.
func (s *RbdReplicationSuite) TestDisableHandlerImageNeedsNoPeer() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "disable", "pool/image_one"}...).Return("", nil).Once()
	common.ProcessExec = r

	err := s.disable(imageHandler(types.RbdResourceImage))
	assert.NoError(s.T(), err)
}

// Multiple sites cannot be disabled by the two-site operation, even when a
// request names one remote or only one of the sites has been imported.
func (s *RbdReplicationSuite) TestDisableHandlerMultiplePeerSites() {
	simple := types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"}
	other := types.RemoteRecord{ID: 2, Name: "other", LocalName: "magical"}
	otherPeer := RbdReplicationPeer{Id: "other-peer", RemoteName: "other"}
	for _, tc := range []struct {
		name    string
		peers   []RbdReplicationPeer
		remotes types.RemoteRecords
		remote  string
		force   bool
	}{
		{name: "unimported peer first", peers: []RbdReplicationPeer{otherPeer, simplePeer}, remotes: types.RemoteRecords{simple}},
		{name: "unimported peer last", peers: []RbdReplicationPeer{simplePeer, otherPeer}, remotes: types.RemoteRecords{simple}},
		{name: "both peers imported", peers: []RbdReplicationPeer{simplePeer, otherPeer}, remotes: types.RemoteRecords{simple, other}},
		{name: "named remote", peers: []RbdReplicationPeer{simplePeer, otherPeer}, remotes: types.RemoteRecords{simple, other}, remote: "simple"},
		{name: "forced disable", peers: []RbdReplicationPeer{simplePeer, otherPeer}, remotes: types.RemoteRecords{simple}, force: true},
	} {
		s.Run(tc.name, func() {
			s.stubRemotes(tc.remotes...)
			s.noCommands()
			rh := poolHandler(types.RbdResourcePool, tc.peers...)
			rh.Request.RemoteName = tc.remote
			rh.Request.IsForceOp = tc.force
			if tc.force {
				rh.PoolStatus.Health = RbdReplicationHealthWarn
			}
			assert.Error(s.T(), s.disable(rh))
		})
	}
}

func (s *RbdReplicationSuite) TestDisableHandlerReregisteredPeer() {
	s.stubRemotes(types.RemoteRecord{ID: 1, Name: "simple", LocalName: "magical"})
	r := mocks.NewRunner(s.T())
	expectNoPoolImages(r)
	expectPoolDisable(r)
	common.ProcessExec = r
	ghostPeer := RbdReplicationPeer{Id: "reregistered-peer", RemoteName: "simple", Direction: "tx-only"}

	err := s.disable(poolHandler(types.RbdResourcePool, simplePeer, ghostPeer))
	assert.NoError(s.T(), err)
}
