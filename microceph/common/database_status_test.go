package common

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

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "service unavailable status with waiting text",
			err:  lxdApi.StatusErrorf(http.StatusServiceUnavailable, waitingMessage),
			want: true,
		},
		{
			name: "status error with waiting text but wrong status",
			err:  lxdApi.StatusErrorf(http.StatusInternalServerError, waitingMessage),
			want: false,
		},
		{
			name: "non-status error with waiting text falls back to string match",
			err:  errors.New(waitingMessage),
			want: true,
		},
		{
			name: "service unavailable status without waiting text",
			err:  lxdApi.StatusErrorf(http.StatusServiceUnavailable, "database is offline"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsDatabaseUpgradeWaitingError(tt.err))
		})
	}
}
