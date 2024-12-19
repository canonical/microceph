package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/ceph"
)

// /osds Endpoint.
var osdCmd = rest.Endpoint{
	Path: "osds",

	Put: rest.EndpointAction{Handler: cmdOsdPut, ProxyTarget: true},
}

// cmdOsdPut stop or start OSD service on a remote target
func cmdOsdPut(s state.State, r *http.Request) response.Response {
	var osdPut types.OsdPut

	err := json.NewDecoder(r.Body).Decode(&osdPut)
	if err != nil {
		logger.Errorf("Failed decoding body: %v", err)
		return response.InternalError(err)
	}

	state := osdPut.State
	switch state {
	case "up":
		err = ceph.SetOsdState(true)
	case "down":
		err = ceph.SetOsdState(false)
	default:
		err = fmt.Errorf("unknown state encounter: %s.", state)
	}

	if err != nil {
		url := s.Address().String()
		logger.Errorf("Failed update the state of osd service on host %s", url)
		return response.SyncResponse(false, err)
	}

	return response.EmptySyncResponse
}
