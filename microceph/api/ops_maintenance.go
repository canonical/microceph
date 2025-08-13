package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/canonical/microceph/microceph/logger"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
	"github.com/gorilla/mux"
)

// Maintenance response.
type maintenanceResponse struct {
	success bool
	content []ceph.Result
}

// Render renders a response for /ops/maintenance/{node} endpoint.
func (r *maintenanceResponse) Render(w http.ResponseWriter, req *http.Request) (err error) {
	debugLogger := logger.NewLXDLoggerAdapter(logger.DaemonLogger)
	w.Header().Set("Content-Type", "application/json")

	var resp api.ResponseRaw
	if !r.success {
		w.WriteHeader(http.StatusBadRequest)
		errMessages := []string{}
		for _, result := range r.content {
			errMessage := result.Error
			if errMessage != "" {
				errMessages = append(errMessages, fmt.Sprintf("(%s)", errMessage))
			}
		}
		resp = api.ResponseRaw{
			Type:     api.ErrorResponse,
			Code:     http.StatusBadRequest,
			Error:    fmt.Sprintf("maintenance operations failed: [%v]", strings.Join(errMessages, " ")),
			Metadata: r.content,
		}
	} else {
		status := api.Success
		resp = api.ResponseRaw{
			Type:       api.SyncResponse,
			Status:     status.String(),
			StatusCode: int(status),
			Metadata:   r.content,
		}
	}

	return util.WriteJSON(w, resp, debugLogger)
}

func (r *maintenanceResponse) String() string {
	if !r.success {
		return "success"
	}
	return "failure"
}

// /ops/maintenance/{node} endpoint.
var opsMaintenanceNodeCmd = rest.Endpoint{
	Path: "ops/maintenance/{node}",
	Put:  rest.EndpointAction{Handler: cmdPutMaintenance, ProxyTarget: true},
}

// cmdPutMaintenance bring a node in or out of maintenance
func cmdPutMaintenance(s state.State, r *http.Request) response.Response {
	var results []ceph.Result
	var maintenanceRequest types.MaintenanceRequest

	node, err := url.PathUnescape(mux.Vars(r)["node"])
	if err != nil {
		return response.BadRequest(err)
	}

	err = json.NewDecoder(r.Body).Decode(&maintenanceRequest)
	if err != nil {
		logger.Errorf("failed decoding body: %v", err)
		return response.InternalError(err)
	}

	maintenance := ceph.Maintenance{
		Node: node,
		ClusterOps: ceph.ClusterOps{
			State:   s,
			Context: r.Context(),
		},
	}

	status := maintenanceRequest.Status
	switch status {
	case "maintenance":
		results, err = maintenance.Enter(maintenanceRequest)
	case "non-maintenance":
		results, err = maintenance.Exit(maintenanceRequest)
	default:
		err = fmt.Errorf("unknown status encounter: '%s', can only be 'maintenance' or 'non-maintenance'", status)
	}

	if err != nil {
		return response.BadRequest(err)
	}

	for _, result := range results {
		if result.Error != "" && !maintenanceRequest.Force {
			return &maintenanceResponse{success: false, content: results}
		}
	}

	return &maintenanceResponse{success: true, content: results}
}
