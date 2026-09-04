package ceph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenMonmapSetsPersistentNautilusFeature(t *testing.T) {
	r := withMockRunner(t)
	r.On(
		"RunCommand",
		"monmaptool",
		"--create",
		"--feature-set",
		"nautilus",
		"--persistent",
		"--fsid",
		"test-fsid",
		"/tmp/mon.map",
	).Return("", nil).Once()

	err := genMonmap("/tmp/mon.map", "test-fsid")
	assert.NoError(t, err)
	r.AssertExpectations(t)
}
