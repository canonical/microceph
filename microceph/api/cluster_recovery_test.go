package api

import (
	"errors"
	"net/http"
	"testing"

	lxdApi "github.com/canonical/lxd/shared/api"
	microTypes "github.com/canonical/microcluster/v2/rest/types"
	"github.com/stretchr/testify/assert"
)

func TestGetForceRemoveRecoveryMode(t *testing.T) {
	waitingMessage := string(microTypes.DatabaseWaiting) + ": 1 cluster members have not yet received the update"

	tests := []struct {
		name     string
		err      error
		wantMode forceRemoveRecoveryMode
		wantErr  bool
	}{
		{
			name:     "database is open uses standard path",
			err:      nil,
			wantMode: forceRemoveRecoveryModeUseStandardPath,
			wantErr:  true,
		},
		{
			name:     "upgrade waiting allows recovery path",
			err:      lxdApi.StatusErrorf(http.StatusServiceUnavailable, waitingMessage),
			wantMode: forceRemoveRecoveryModeAllowed,
			wantErr:  false,
		},
		{
			name:     "non waiting database error is unavailable",
			err:      errors.New("database is offline"),
			wantMode: forceRemoveRecoveryModeUnavailable,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := getForceRemoveRecoveryMode(tt.err)
			assert.Equal(t, tt.wantMode, mode)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
