package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestEnableServiceFailureReturnsHTTPError(t *testing.T) {
	request := httptest.NewRequest(http.MethodPut, "/1.0/services/smb", strings.NewReader(`{"name":"not-a-service","bool":true}`))
	recorder := httptest.NewRecorder()
	response := cmdEnableServicePut(nil, request)
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	var result struct {
		Type      string `json:"type"`
		Error     string `json:"error"`
		ErrorCode int    `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, "error", result.Type)
	require.Equal(t, http.StatusInternalServerError, result.ErrorCode)
	require.Contains(t, result.Error, "enablement is not supported")
}

func TestSMBServiceGroupGetReturnsEmptySnapshotWhenAbsentOrFinalized(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
CREATE TABLE service_groups (id INTEGER PRIMARY KEY, service TEXT, group_id TEXT, config TEXT);
CREATE TABLE grouped_services (id INTEGER PRIMARY KEY, service_group_id INTEGER);
INSERT INTO service_groups VALUES (1, 'smb', 'files', '{}'), (2, 'smb', 'empty-config', '');`)
	require.NoError(t, err)

	state := &mocks.MockState{
		Cert: &shared.CertInfo{},
		DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			err = f(ctx, tx)
			if err == nil {
				return tx.Commit()
			}
			require.NoError(t, tx.Rollback())
			return err
		}},
	}
	getGroup := func(clusterID string) types.SMBServiceGroup {
		request := httptest.NewRequest(http.MethodGet, "/1.0/services/smb?cluster_id="+clusterID, nil)
		recorder := httptest.NewRecorder()
		response := cmdSMBServiceGroupGet(state, request)
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusOK, recorder.Code)
		var result struct {
			Metadata types.SMBServiceGroup `json:"metadata"`
		}
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&result))
		return result.Metadata
	}

	// A never-deployed cluster has no persisted group.
	require.Equal(t, types.SMBServiceGroup{ClusterID: "empty"}, getGroup("empty"))
	require.Equal(t, types.SMBServiceGroup{ClusterID: "files", GroupConfig: "{}"}, getGroup("files"))
	// Explicit finalization must work even when the captured configuration is empty.
	for _, body := range []string{
		`{"cluster_id":"files","finalize":true,"group_config":"{}"}`,
		`{"cluster_id":"empty-config","finalize":true}`,
		`{"cluster_id":"files","finalize":true}`,
	} {
		request := httptest.NewRequest(http.MethodDelete, "/1.0/services/smb", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		response := cmdSMBDeleteService(state, request)
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusOK, recorder.Code)
	}
	require.Equal(t, types.SMBServiceGroup{ClusterID: "files"}, getGroup("files"))
	var groups int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups`).Scan(&groups))
	require.Zero(t, groups)
}

func TestServicesGetExposesGroupedServiceConfiguration(t *testing.T) {
	state := &mocks.MockState{}
	serviceQuery := mocks.NewServiceQueryInterface(t)
	serviceQuery.On("List", mock.Anything, state).Return(types.Services{}, nil).Once()
	groupedServiceQuery := mocks.NewGroupedServiceQueryIntf(t)
	groupedServiceQuery.On("GetGroupedServicesWithGroupConfig", mock.Anything, mock.Anything).Return(
		[]database.GroupedServiceWithGroupConfig{
			{
				GroupedService: database.GroupedService{
					Service: "smb",
					GroupID: "files",
					Member:  "node-a",
					Info:    `{"config_uri":"rados://.smb/files/config.smb"}`,
				},
				GroupConfig: `{"desired_spec":{"service_type":"smb","service_id":"files"}}`,
			},
		}, nil,
	).Once()

	originalServiceQuery := database.ServiceQuery
	originalGroupedServiceQuery := database.GroupedServicesQuery
	t.Cleanup(func() {
		database.ServiceQuery = originalServiceQuery
		database.GroupedServicesQuery = originalGroupedServiceQuery
	})
	database.ServiceQuery = serviceQuery
	database.GroupedServicesQuery = groupedServiceQuery

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/1.0/services", nil)
	response := cmdServicesGet(state, request)
	_ = response.Render(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Metadata types.Services `json:"metadata"`
	}
	err := json.NewDecoder(recorder.Body).Decode(&result)
	require.NoError(t, err)
	require.Equal(t, types.Services{
		{
			Service:     "smb",
			Location:    "node-a",
			GroupID:     "files",
			Info:        `{"config_uri":"rados://.smb/files/config.smb"}`,
			GroupConfig: `{"desired_spec":{"service_type":"smb","service_id":"files"}}`,
		},
	}, result.Metadata)
}

func TestSMBServiceLocalStateUsesMarkerWithoutGroupReceipt(t *testing.T) {
	originalGetLocalClusterID := getLocalSMBClusterIDFunc
	t.Cleanup(func() {
		getLocalSMBClusterIDFunc = originalGetLocalClusterID
	})
	getLocalSMBClusterIDFunc = func(context.Context) (string, error) {
		return "files", nil
	}

	request := httptest.NewRequest(http.MethodGet, "/1.0/services/smb?cluster_id=files&local=true&target=node-a", nil)
	recorder := httptest.NewRecorder()
	response := cmdSMBServiceGroupGet(&mocks.MockState{}, request)
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusOK, recorder.Code)

	var result struct {
		Metadata types.SMBLocalState `json:"metadata"`
	}
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&result))
	require.Equal(t, types.SMBLocalState{ClusterID: "files", LocalClusterID: "files"}, result.Metadata)
}
