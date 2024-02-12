package api

import (
	"encoding/json"
	"fmt"
	"github.com/canonical/microceph/microceph/interfaces"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/canonical/lxd/shared/logger"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/disks endpoint.
var disksCmd = rest.Endpoint{
	Path: "disks",

	Get:  rest.EndpointAction{Handler: cmdDisksGet, ProxyTarget: true},
	Post: rest.EndpointAction{Handler: cmdDisksPost, ProxyTarget: true},
}

// /1.0/disks/{osdid} endpoint.
var disksDelCmd = rest.Endpoint{
	Path: "disks/{osdid}",

	Delete: rest.EndpointAction{Handler: cmdDisksDelete, ProxyTarget: true},
}

var mu sync.Mutex

func cmdDisksGet(s *state.State, r *http.Request) response.Response {
	disks, err := ceph.ListOSD(s)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, disks)
}

func cmdDisksPost(s *state.State, r *http.Request) response.Response {
	var req types.DisksPost
	var wal *types.DiskParameter
	var db *types.DiskParameter
	var disks []types.DiskParameter

	logger.Infof("cmdDisksPost: %v", req)
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	mu.Lock()
	defer mu.Unlock()

	// No usable diskpath were provided.
	if len(req.Path) == 0 {
		return response.SyncResponse(true, types.DiskAddResponse{})
	}

	// prepare a slice of disk parameters for requested disks or loop spec.
	disks = make([]types.DiskParameter, len(req.Path))
	for i, diskPath := range req.Path {
		disks[i] = types.DiskParameter{
			Path:     diskPath,
			Encrypt:  req.Encrypt,
			Wipe:     req.Wipe,
			LoopSize: 0,
		}
	}

	if req.WALDev != nil {
		wal = &types.DiskParameter{Path: *req.WALDev, Encrypt: req.WALEncrypt, Wipe: req.WALWipe, LoopSize: 0}
	}

	if req.DBDev != nil {
		db = &types.DiskParameter{Path: *req.DBDev, Encrypt: req.DBEncrypt, Wipe: req.DBWipe, LoopSize: 0}
	}

	resp := ceph.AddBulkDisks(s, disks, wal, db)
	if len(resp.ValidationError) == 0 {
		response.SyncResponse(false, resp)
	}

	return response.SyncResponse(true, resp)
}

// cmdDisksDelete is the handler for DELETE /1.0/disks/{osdid}.
func cmdDisksDelete(s *state.State, r *http.Request) response.Response {
	var osd string
	osd, err := url.PathUnescape(mux.Vars(r)["osdid"])
	if err != nil {
		return response.BadRequest(err)
	}

	var req types.DisksDelete
	osdid, err := strconv.ParseInt(osd, 10, 64)

	if err != nil {
		return response.BadRequest(err)
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	mu.Lock()
	defer mu.Unlock()

	cs := interfaces.CephState{State: s}
	needDowngrade, err := ceph.IsDowngradeNeeded(cs, osdid)
	if err != nil {
		return response.InternalError(err)
	}
	if needDowngrade && !req.ConfirmDowngrade {
		errorMsg := fmt.Errorf(
			"Removing osd.%s would require a downgrade of the automatic crush rule from 'host' to 'osd' level. "+
				"Likely this will result in additional data movement. Please confirm by setting the "+
				"'--confirm-failure-domain-downgrade' flag to true",
			osd,
		)
		return response.BadRequest(errorMsg)
	}

	err = ceph.RemoveOSD(cs, osdid, req.BypassSafety, req.Timeout)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
