package api

import (
	"encoding/json"
	"fmt"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/gorilla/mux"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

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
	var data types.DiskParameter

	logger.Debugf("cmdDisksPost: %v", req)
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	mu.Lock()
	defer mu.Unlock()

	// check if we want OSDs backed by files
	if strings.HasPrefix(req.Path, "loop,") {
		logger.Debugf("cmdDisksPost: adding loopback OSDs")
		err = ceph.AddLoopBackOSDs(s, req.Path)
		if err != nil {
			return response.SmartError(err)
		}
		return response.EmptySyncResponse
	}

	// handle physical devices
	data = types.DiskParameter{req.Path, req.Encrypt, req.Wipe, 0}
	if req.WALDev != nil {
		wal = &types.DiskParameter{*req.WALDev, req.WALEncrypt, req.WALWipe, 0}
	}
	if req.DBDev != nil {
		db = &types.DiskParameter{*req.DBDev, req.DBEncrypt, req.DBWipe, 0}
	}

	// add a regular block device
	err = ceph.AddOSD(s, data, wal, db)
	if err != nil {
		return response.SmartError(err)
	}
	logger.Debugf("cmdDisksPost done: %v", req)
	return response.EmptySyncResponse
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

	cs := common.CephState{State: s}
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
