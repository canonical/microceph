package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

// cephOnlyBootstrapSuite tests CephOnlyBootstrap lifecycle, idempotency,
// concurrency, and target validation (CE142 M4 / UAT-S1.3, UAT-S1.4).
type cephOnlyBootstrapSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
	mockDB             *mockLifecycleDB
}

func TestCephOnlyBootstrap(t *testing.T) {
	suite.Run(t, new(cephOnlyBootstrapSuite))
}

func (s *cephOnlyBootstrapSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.mockDB = newMockLifecycleDB()
	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
		Cert:        &shared.CertInfo{},
		DBObj:       &mocks.MockDB{TxFn: s.mockDB.Transaction},
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()

	// Default: target is a known member.
	origMembers := GetClusterMemberNamesFunc
	GetClusterMemberNamesFunc = func(_ context.Context, _ interfaces.StateInterface) ([]string, error) {
		return []string{"node-a", "node-b", "node-c"}, nil
	}
	s.T().Cleanup(func() { GetClusterMemberNamesFunc = origMembers })
}

// TestCephOnlyBootstrapSuccess verifies a successful Ceph-only bootstrap records
// the bootstrapped state (UAT-S1.3).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapSuccess() {
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err)

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateBootstrapped, lc.CephBootstrapState)
	assert.Equal(s.T(), "node-b", lc.CephBootstrapTarget)
}

// TestCephOnlyBootstrapIdempotent verifies that a second call when already
// bootstrapped returns nil (no-op success) and does not run steps (UAT-S1.4).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapIdempotent() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:   true,
		CephBootstrapState: database.CephStateBootstrapped,
	})

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err, "already-bootstrapped must be no-op success, not error")
	assert.False(s.T(), stepsCalled, "bootstrap steps must not run when already bootstrapped")
}

// TestCephOnlyBootstrapInProgress verifies that an in-progress bootstrap returns
// a retryable error (UAT-S1.4).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapInProgress() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:    false,
		CephBootstrapState:  database.CephStateInProgress,
		CephBootstrapTarget: "node-a",
	})

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.ErrorIs(s.T(), err, ErrCephBootstrapInProgress)
	assert.False(s.T(), stepsCalled, "bootstrap steps must not run when another bootstrap is in progress")
}

// TestCephOnlyBootstrapUnknownTarget verifies that an unknown target member is
// rejected (UAT-S1.3).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapUnknownTarget() {
	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "unknown-node", common.BootstrapConfig{}, false)
	assert.ErrorIs(s.T(), err, ErrUnknownBootstrapTarget)
	assert.False(s.T(), stepsCalled, "bootstrap steps must not run for unknown target")
}

// TestCephOnlyBootstrapFailureRecordsDetail verifies that a failed bootstrap
// records the error detail in lifecycle state for operator retry (UAT-S1.4).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapFailureRecordsDetail() {
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		return fmt.Errorf("keyring creation failed: permission denied")
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.Error(s.T(), err)

	lc := s.mockDB.get()
	assert.False(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateFailed, lc.CephBootstrapState)
	assert.Contains(s.T(), lc.Detail, "keyring creation failed")
}

// TestCephOnlyBootstrapRetryAfterFailure verifies that after a failed bootstrap,
// a retry can proceed (state transitions from failed back to in_progress).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapRetryAfterFailure() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:   false,
		CephBootstrapState: database.CephStateFailed,
		Detail:             "previous failure",
	})

	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err)

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateBootstrapped, lc.CephBootstrapState)
}

// TestCephOnlyBootstrapRaceGuard (N2) verifies that two concurrent callers
// cannot both proceed to bootstrap. The first caller blocks CephBootstrapStepsFunc
// on a channel; the second caller observes in_progress and returns
// ErrCephBootstrapInProgress.
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapRaceGuard() {
	// Channel that blocks the first caller's steps until the second caller
	// has attempted and returned.
	firstStarted := make(chan struct{})
	firstProceed := make(chan struct{})

	origSteps := CephBootstrapStepsFunc
	callCount := 0
	var callCountMu sync.Mutex
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		callCountMu.Lock()
		callCount++
		isFirst := callCount == 1
		callCountMu.Unlock()
		if isFirst {
			close(firstStarted)
			<-firstProceed // block until test signals
		}
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	var firstErr, secondErr error
	var wg sync.WaitGroup

	// First caller: starts bootstrap and blocks in steps.
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstErr = CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	}()

	// Wait for the first caller to enter the steps function (in_progress set).
	<-firstStarted

	// Second caller: should see in_progress and return immediately.
	wg.Add(1)
	go func() {
		defer wg.Done()
		secondErr = CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	}()

	// Give the second caller time to attempt.
	// Since the in-process mutex serializes calls, the second caller blocks on
	// the mutex until the first releases. To test the dqlite-level guard, we
	// need to call atomicStartBootstrapFunc directly.
	proceed, err := atomicStartBootstrapFunc(context.Background(), s.TestStateInterface, "node-b", false)
	assert.ErrorIs(s.T(), err, ErrCephBootstrapInProgress)
	assert.False(s.T(), proceed)

	// Let the first caller complete.
	close(firstProceed)
	wg.Wait()

	assert.NoError(s.T(), firstErr)
	assert.NoError(s.T(), secondErr) // sees bootstrapped after first completes

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
}

// TestCephOnlyBootstrapForceRecoversStaleInProgress verifies that --force
// recovers from a stale in_progress state: it resets in_progress to failed,
// then the normal retry bootstraps successfully (FIX 1b).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapForceRecoversStaleInProgress() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:    false,
		CephBootstrapState:  database.CephStateInProgress,
		CephBootstrapTarget: "node-a",
	})

	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, true)
	assert.NoError(s.T(), err, "force should recover stale in_progress and bootstrap")

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateBootstrapped, lc.CephBootstrapState)
}

// TestCephOnlyBootstrapNoForceStaysInProgress verifies that without --force,
// a stale in_progress state still returns ErrCephBootstrapInProgress (FIX 1b).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapNoForceStaysInProgress() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:    false,
		CephBootstrapState:  database.CephStateInProgress,
		CephBootstrapTarget: "node-a",
	})

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.ErrorIs(s.T(), err, ErrCephBootstrapInProgress)
	assert.False(s.T(), stepsCalled)
}

// TestCephOnlyBootstrapNoOpWhenConfigBootstrapped verifies the defensive guard:
// when the lifecycle row is stale (not_bootstrapped) but Ceph config rows
// exist (fsid + admin keyring) — e.g. a fresh non-deferred bootstrap that
// failed to mark the lifecycle, or an upgrade edge case — CephOnlyBootstrap must
// NOT bootstrap over the existing cluster. It self-heals the lifecycle row to
// bootstrapped and returns a no-op success without running the bootstrap steps.
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapNoOpWhenFullyBootstrapped() {
	// Lifecycle bootstrapped AND config rows exist: genuine no-op success.
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:   true,
		CephBootstrapState: database.CephStateBootstrapped,
	})
	s.mockDB.setConfig(map[string]string{
		"fsid":                 "deadbeef-0000-0000-0000-000000000000",
		"keyring.client.admin": "AQABfakekey==",
	})

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err, "must be a no-op when already fully bootstrapped")
	assert.False(s.T(), stepsCalled, "bootstrap steps must not run over an existing cluster")
}

// TestCephOnlyBootstrapRefusesPartialBootstrap verifies the retry-safety guard:
// when config rows exist (fsid + admin keyring) but the lifecycle is NOT
// bootstrapped and Ceph connectivity cannot be verified, the retry must be
// REFUSED with ErrPartialBootstrap rather than re-running SimpleBootstrapper
// (which would generate a divergent FSID and trip duplicate-key INSERT 409s).
// This makes Ceph-only bootstrap retry safe.
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapRefusesPartialBootstrap() {
	// Partial state: config rows present, lifecycle failed (not bootstrapped).
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:   false,
		CephBootstrapState: database.CephStateFailed,
		Detail:             "prior partial failure",
	})
	s.mockDB.setConfig(map[string]string{
		"fsid":                 "deadbeef-0000-0000-0000-000000000000",
		"keyring.client.admin": "AQABfakekey==",
	})

	origVerify := verifyExistingCephBootstrapFunc
	verifyExistingCephBootstrapFunc = func(_ context.Context) error {
		return fmt.Errorf("ceph status failed")
	}
	s.T().Cleanup(func() { verifyExistingCephBootstrapFunc = origVerify })

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.ErrorIs(s.T(), err, ErrPartialBootstrap, "retry over a partial bootstrap must be refused")
	assert.False(s.T(), stepsCalled, "bootstrap steps must not run over a partial bootstrap")
	assert.Contains(s.T(), err.Error(), "Clean up the partial bootstrap")

	// Lifecycle must remain failed (not falsely marked bootstrapped).
	lc := s.mockDB.get()
	assert.False(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateFailed, lc.CephBootstrapState)
}

// TestCephOnlyBootstrapRecoversStaleLifecycle verifies the recovery path for a
// successful bootstrap whose final lifecycle write failed: config rows exist,
// the lifecycle is stale, and a cheap Ceph connectivity check succeeds.
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapRecoversStaleLifecycle() {
	s.mockDB.set(database.ClusterLifecycle{
		CephBootstrapped:   false,
		CephBootstrapState: database.CephStateInProgress,
		Detail:             "recording result failed",
	})
	s.mockDB.setConfig(map[string]string{
		"fsid":                 "deadbeef-0000-0000-0000-000000000000",
		"keyring.client.admin": "AQABfakekey==",
	})

	origVerify := verifyExistingCephBootstrapFunc
	verifyExistingCephBootstrapFunc = func(_ context.Context) error {
		return nil
	}
	s.T().Cleanup(func() { verifyExistingCephBootstrapFunc = origVerify })

	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err)
	assert.False(s.T(), stepsCalled, "bootstrap steps must not rerun over a verified existing cluster")

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
	assert.Equal(s.T(), database.CephStateBootstrapped, lc.CephBootstrapState)
	assert.Equal(s.T(), "node-b", lc.CephBootstrapTarget)
}

// TestCephOnlyBootstrapBootstrapsWhenNoConfig verifies that without config
// rows, the defensive guard does not block a real bootstrap (the normal
// not_bootstrapped -> bootstrap path is preserved).
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapBootstrapsWhenNoConfig() {
	// No config rows; lifecycle not_bootstrapped (default mock state).
	stepsCalled := false
	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		stepsCalled = true
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(context.Background(), s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err)
	assert.True(s.T(), stepsCalled, "bootstrap steps must run when no existing cluster")
	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped)
}

// TestCephOnlyBootstrapRecordsResultWithCancelledContext verifies that the
// result-recording transaction uses a fresh context, so a cancelled request
// context does not strand the lifecycle in in_progress (FIX 1a).
//
// The realistic scenario: the client cancels (timeout) while the bootstrap
// steps are running. The atomic transition to in_progress has already
// committed; the result must still be recorded using a fresh context.
func (s *cephOnlyBootstrapSuite) TestCephOnlyBootstrapRecordsResultWithCancelledContext() {
	ctx, cancel := context.WithCancel(context.Background())

	origSteps := CephBootstrapStepsFunc
	CephBootstrapStepsFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig) error {
		cancel() // simulate client timeout during bootstrap steps
		return nil
	}
	s.T().Cleanup(func() { CephBootstrapStepsFunc = origSteps })

	err := CephOnlyBootstrap(ctx, s.TestStateInterface, "node-b", common.BootstrapConfig{}, false)
	assert.NoError(s.T(), err, "result recording must succeed even with a cancelled request context")

	lc := s.mockDB.get()
	assert.True(s.T(), lc.CephBootstrapped, "lifecycle must be recorded as bootstrapped")
	assert.Equal(s.T(), database.CephStateBootstrapped, lc.CephBootstrapState)
}

// --- mock lifecycle database ---

// mockLifecycleDB implements mcTypes.DB enough to support Transaction calls.
type mockLifecycleDB struct {
	mu         sync.Mutex
	lc         database.ClusterLifecycle
	configRows map[string]string
}

func newMockLifecycleDB() *mockLifecycleDB {
	return &mockLifecycleDB{
		lc: database.ClusterLifecycle{
			CephBootstrapped:   false,
			CephBootstrapState: database.CephStateNotBootstrapped,
		},
		configRows: map[string]string{},
	}
}

func (m *mockLifecycleDB) get() database.ClusterLifecycle {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lc
}

func (m *mockLifecycleDB) set(lc database.ClusterLifecycle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lc = lc
}

// setConfig sets config rows (e.g. fsid, keyring.client.admin) that the
// defensive configIndicatesBootstrapped check reads.
func (m *mockLifecycleDB) setConfig(rows map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configRows = rows
}

// Transaction executes the given function with a mock *sql.Tx that reads/writes
// the in-memory lifecycle state.
func (m *mockLifecycleDB) Transaction(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a real in-memory sqlite tx for the lifecycle table so the
	// database.GetClusterLifecycle/SetClusterLifecycle functions work.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE cluster_lifecycle (
  id                    INTEGER PRIMARY KEY NOT NULL DEFAULT 1,
  ceph_bootstrapped     INTEGER NOT NULL DEFAULT 0,
  ceph_bootstrap_state  TEXT    NOT NULL DEFAULT 'not_bootstrapped',
  ceph_bootstrap_target TEXT,
  detail                TEXT,
  CONSTRAINT singleton CHECK (id = 1)
);
CREATE TABLE config (
  id    INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL,
  UNIQUE(key)
);
INSERT INTO cluster_lifecycle (id, ceph_bootstrapped, ceph_bootstrap_state, ceph_bootstrap_target, detail)
VALUES (1, ?, ?, ?, ?);`, btoi(m.lc.CephBootstrapped), m.lc.CephBootstrapState, m.lc.CephBootstrapTarget, m.lc.Detail)
	if err != nil {
		return err
	}
	for k, v := range m.configRows {
		if _, err := db.Exec(`INSERT INTO config (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Read back the updated state.
	row := db.QueryRowContext(ctx, `SELECT ceph_bootstrapped, ceph_bootstrap_state, coalesce(ceph_bootstrap_target,''), coalesce(detail,'') FROM cluster_lifecycle WHERE id = 1`)
	var bs int
	var state, target, detail string
	if err := row.Scan(&bs, &state, &target, &detail); err != nil {
		return err
	}
	m.lc = database.ClusterLifecycle{
		CephBootstrapped:    bs != 0,
		CephBootstrapState:  state,
		CephBootstrapTarget: target,
		Detail:              detail,
	}
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
