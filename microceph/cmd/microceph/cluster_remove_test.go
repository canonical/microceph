package main

import (
	"errors"
	"net/http"
	"testing"

	lxdApi "github.com/canonical/lxd/shared/api"
	microTypes "github.com/canonical/microcluster/v2/rest/types"
	"github.com/stretchr/testify/assert"
)

func TestIsDatabaseUpgradeWaitingError(t *testing.T) {
	waitingMessage := string(microTypes.DatabaseWaiting) + ": 1 cluster members have not yet received the update"

	assert.True(t, isDatabaseUpgradeWaitingError(lxdApi.StatusErrorf(http.StatusServiceUnavailable, waitingMessage)))
	assert.False(t, isDatabaseUpgradeWaitingError(lxdApi.StatusErrorf(http.StatusInternalServerError, waitingMessage)))
	assert.True(t, isDatabaseUpgradeWaitingError(errors.New(waitingMessage))) // compatibility fallback
	assert.False(t, isDatabaseUpgradeWaitingError(errors.New("some other failure")))
	assert.False(t, isDatabaseUpgradeWaitingError(nil))
}
