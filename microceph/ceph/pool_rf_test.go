package ceph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
)

// withRunner installs r as the process runner for the duration of the test.
func withRunner(t *testing.T, r *mocks.Runner) {
	orig := common.ProcessExec
	common.ProcessExec = r
	t.Cleanup(func() { common.ProcessExec = orig })
}

// TestSetReplicationFactorWithDefault checks that the default pool size is set along with the pools
// when asked to.
func TestSetReplicationFactorWithDefault(t *testing.T) {
	r := mocks.NewRunner(t)
	r.On("RunCommand", "ceph", "config", "set", "global", "osd_pool_default_size", "2").Return("", nil).Once()
	r.On("RunCommand", "ceph", "config", "set", "global", "mon_allow_pool_size_one", "false").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "pool", "set", "mypool", "size", "2", "--yes-i-really-mean-it").Return("", nil).Once()
	withRunner(t, r)

	err := SetReplicationFactor([]string{"mypool"}, 2, true)
	require.NoError(t, err)
}

// TestSetReplicationFactorOnlyPools checks that changing the size of a pool leaves the default pool
// size and mon_allow_pool_size_one alone. The runner mock fails the test on any other call.
func TestSetReplicationFactorOnlyPools(t *testing.T) {
	r := mocks.NewRunner(t)
	r.On("RunCommand", "ceph", "osd", "pool", "set", "mypool", "size", "3", "--yes-i-really-mean-it").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "pool", "set", "other", "size", "3", "--yes-i-really-mean-it").Return("", nil).Once()
	withRunner(t, r)

	err := SetReplicationFactor([]string{"mypool", "other"}, 3, false)
	require.NoError(t, err)
}

// TestSetReplicationFactorAllPools checks that "*" resizes every existing pool without touching the
// default pool size, as documented.
func TestSetReplicationFactorAllPools(t *testing.T) {
	r := mocks.NewRunner(t)
	r.On("RunCommand", "ceph", "osd", "pool", "ls", "--format", "json").Return(`["a","b"]`, nil).Once()
	r.On("RunCommand", "ceph", "osd", "pool", "set", "a", "size", "2", "--yes-i-really-mean-it").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "pool", "set", "b", "size", "2", "--yes-i-really-mean-it").Return("", nil).Once()
	withRunner(t, r)

	err := SetReplicationFactor([]string{"*"}, 2, false)
	require.NoError(t, err)
}

// TestSetReplicationFactorSizeOneAllowsIt checks that resizing a pool to one still enables
// mon_allow_pool_size_one, which Ceph requires, without setting the default pool size.
func TestSetReplicationFactorSizeOneAllowsIt(t *testing.T) {
	r := mocks.NewRunner(t)
	r.On("RunCommand", "ceph", "config", "set", "global", "mon_allow_pool_size_one", "true").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "pool", "set", "mypool", "size", "1", "--yes-i-really-mean-it").Return("", nil).Once()
	withRunner(t, r)

	err := SetReplicationFactor([]string{"mypool"}, 1, false)
	require.NoError(t, err)
}

// TestSetReplicationFactorDefaultOnly checks the legacy empty pool name, which sets the default pool
// size without changing any pool.
func TestSetReplicationFactorDefaultOnly(t *testing.T) {
	r := mocks.NewRunner(t)
	r.On("RunCommand", "ceph", "config", "set", "global", "osd_pool_default_size", "1").Return("", nil).Once()
	r.On("RunCommand", "ceph", "config", "set", "global", "mon_allow_pool_size_one", "true").Return("", nil).Once()
	withRunner(t, r)

	err := SetReplicationFactor([]string{""}, 1, true)
	assert.NoError(t, err)
}
