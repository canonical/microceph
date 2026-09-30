package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
