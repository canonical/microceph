package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

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

func cmdDisksGet(s state.State, r *http.Request) response.Response {
	disks, err := ceph.ListOSD(r.Context(), s)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, disks)
}

func cmdDisksPost(s state.State, r *http.Request) response.Response {
	var req types.DisksPost
	var wal *types.DiskParameter
	var db *types.DiskParameter
	var disks []types.DiskParameter

	req, err := parseAndPatchDiskPostParams(r.Body)
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

	resp := ceph.AddBulkDisks(r.Context(), s, disks, wal, db)
	if len(resp.ValidationError) == 0 {
		response.SyncResponse(false, resp)
	}

	return response.SyncResponse(true, resp)
}

// cmdDisksDelete is the handler for DELETE /1.0/disks/{osdid}.
func cmdDisksDelete(s state.State, r *http.Request) response.Response {
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

	// if check for crush rule scaledown only if crush change is not prohibited.
	if !req.ProhibitCrushScaledown {
		needDowngrade, err := ceph.IsDowngradeNeeded(r.Context(), cs, osdid)
		if err != nil {
			return response.InternalError(err)
		}
		if needDowngrade && !req.ConfirmDowngrade {
			errorMsg := fmt.Errorf(
				"removing osd.%s would require a downgrade of the automatic crush rule from 'host' to 'osd' level. "+
					"Likely this will result in additional data movement. Please confirm by setting the "+
					"'--confirm-failure-domain-downgrade' flag to true",
				osd,
			)
			return response.BadRequest(errorMsg)
		}
	}

	err = ceph.RemoveOSD(r.Context(), cs, osdid, req.BypassSafety, req.Timeout)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// parseAndPatchDiskPostParams parses/patches Disk add command parameters
// to keep the API compatible with older clients.
func parseAndPatchDiskPostParams(rb io.ReadCloser) (types.DisksPost, error) {
	var patchedBody string
	output := types.DisksPost{}

	body, err := io.ReadAll(rb)
	if err != nil {
		return types.DisksPost{}, err
	}

	buf := string(body)

	logger.Debugf("CmdDiskPost Req Body: %v", buf)

	diskPath := gjson.Get(buf, "path")
	if !diskPath.IsArray() {
		patchedBody, err = sjson.Set(buf, "path", []string{diskPath.String()})
		if err != nil {
			return types.DisksPost{}, err
		}
	} else {
		// use unpatched buffer if client is using Batch Disk params.
		patchedBody = buf
	}

	logger.Debugf("CmdDiskPost Patched Body: %v", patchedBody)

	err = json.Unmarshal([]byte(patchedBody), &output)
	if err != nil {
		return types.DisksPost{}, err
	}

	return output, nil
}
