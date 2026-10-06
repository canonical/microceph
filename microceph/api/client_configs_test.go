package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestCmdClientConfigsPut(t *testing.T) {
	origUpdate := updateConfigFunc
	defer func() { updateConfigFunc = origUpdate }()

	// Pretend bootstrapped: the fsid check's transaction succeeds without
	// running against a real database.
	state := &mocks.MockState{DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
		return nil
	}}}

	// 1. UpdateConfig failure must surface as an internal error: the missing
	//    return used to report success while the member kept stale files.
	updateConfigFunc = func(ctx context.Context, s interfaces.StateInterface) error {
		return errors.New("render failed")
	}
	req := httptest.NewRequest(http.MethodPut, "/1.0/client/configs", nil)
	rec := httptest.NewRecorder()
	resp := cmdClientConfigsPut(state, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// 2. Success renders the files and reports success.
	updateConfigFunc = func(ctx context.Context, s interfaces.StateInterface) error {
		return nil
	}
	req = httptest.NewRequest(http.MethodPut, "/1.0/client/configs", nil)
	rec = httptest.NewRecorder()
	resp = cmdClientConfigsPut(state, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
}
