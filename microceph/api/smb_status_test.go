package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

type smbStatusNoCommands struct{ t *testing.T }

func (r smbStatusNoCommands) RunCommand(string, ...string) (string, error) {
	r.t.Fatal("status must not issue commands for SQL-only ownership")
	return "", nil
}
func (r smbStatusNoCommands) RunCommandContext(context.Context, string, ...string) (string, error) {
	r.t.Fatal("status must not issue commands for SQL-only ownership")
	return "", nil
}

func TestSMBStatusPublicSQLOnlyReservationIsVisibleAndReadOnly(t *testing.T) {
	for _, mode := range []string{"fresh", "reserved", "ambiguous", "discovery-error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SNAP_COMMON", t.TempDir())
			db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "status.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			_, err = db.Exec(`CREATE TABLE core_cluster_members(id INTEGER PRIMARY KEY,name TEXT,address TEXT); INSERT INTO core_cluster_members VALUES(1,'a','192.0.2.1');`)
			require.NoError(t, err)
			tx, err := db.Begin()
			require.NoError(t, err)
			require.NoError(t, database.SchemaExtensions[5](context.Background(), tx))
			if mode != "fresh" {
				require.NoError(t, database.ReserveSMBOwner(context.Background(), tx, "alpha", "a"))
			}
			require.NoError(t, tx.Commit())
			if mode == "ambiguous" {
				_, err = db.Exec(`INSERT INTO service_groups(service,group_id,config) VALUES('smb','other','{"owners":{"1":"a"}}')`)
				require.NoError(t, err)
			}
			var before string
			require.NoError(t, db.QueryRow("SELECT group_concat(config) FROM (SELECT config FROM service_groups UNION ALL SELECT 'sentinel')").Scan(&before))
			old := common.ProcessExec
			common.ProcessExec = smbStatusNoCommands{t}
			t.Cleanup(func() { common.ProcessExec = old })
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), smbStatusContextKey{}, "request"), 10*time.Second)
			defer cancel()
			calls := 0
			state := &mocks.MockState{ClusterName: "a", DBObj: &mocks.MockDB{TxFn: func(got context.Context, fn func(context.Context, *sql.Tx) error) error {
				calls++
				require.Equal(t, "request", got.Value(smbStatusContextKey{}))
				deadline, ok := got.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 10*time.Second)
				if mode == "discovery-error" {
					return errors.New("private-source-password")
				}
				if got.Err() != nil {
					return got.Err()
				}
				tx, err := db.Begin()
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback() }()
				return fn(got, tx) // Deliberately never commit a status transaction.
			}}}
			if mode == "cancelled" {
				cancel()
			}
			request := httptest.NewRequest(http.MethodGet, "/1.0/services/smb", nil).WithContext(ctx)
			response := cmdSMBGet(state, request)
			recorder := httptest.NewRecorder()
			require.NoError(t, response.Render(recorder, request))
			require.NotContains(t, recorder.Body.String(), "private-source-password")
			if mode == "fresh" || mode == "reserved" {
				require.Equal(t, http.StatusOK, recorder.Code)
				var envelope struct {
					Metadata types.SMBStatus `json:"metadata"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
				if mode == "fresh" {
					require.Empty(t, envelope.Metadata.ClusterID)
					require.Empty(t, envelope.Metadata.Error)
				} else {
					require.Equal(t, "alpha", envelope.Metadata.ClusterID)
					require.Contains(t, envelope.Metadata.Error, "uncertain")
				}
				require.False(t, envelope.Metadata.Ready)
			} else {
				require.NotEqual(t, http.StatusOK, recorder.Code)
			}
			require.Equal(t, 1, calls)
			var after string
			require.NoError(t, db.QueryRow("SELECT group_concat(config) FROM (SELECT config FROM service_groups UNION ALL SELECT 'sentinel')").Scan(&after))
			require.Equal(t, before, after)
		})
	}
}

type smbStatusContextKey struct{}
