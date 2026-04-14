package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/disks endpoint.
var disksCmd = mcTypes.Endpoint{
	Path: "disks",

	Get:  mcTypes.EndpointAction{Handler: cmdDisksGet, ProxyTarget: true},
	Post: mcTypes.EndpointAction{Handler: cmdDisksPost, ProxyTarget: true},
}

// /1.0/disks/{osdid} endpoint.
var disksDelCmd = mcTypes.Endpoint{
	Path: "disks/{osdid}",

	Delete: mcTypes.EndpointAction{Handler: cmdDisksDelete, ProxyTarget: true},
}

var mu sync.Mutex

func cmdDisksGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	disks, err := ceph.ListOSD(r.Context(), s)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, disks)
}

func cmdDisksPost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.DisksPost
	var wal *types.DiskParameter
	var db *types.DiskParameter
	var disks []types.DiskParameter

	req, err := parseAndPatchDiskPostParams(r.Body)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	err = validateDiskPostRequest(req)
	if err != nil {
		return mcTypes.SyncResponse(true, types.DiskAddResponse{ValidationError: err.Error()})
	}

	mu.Lock()
	defer mu.Unlock()

	// Handle DSL-based device selection.
	if usesDSLDiskAddRequest(req) {
		return handleDSLDiskAdd(r, s, req)
	}

	// No usable diskpath were provided.
	if len(req.Path) == 0 {
		return mcTypes.SyncResponse(true, types.DiskAddResponse{})
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
		mcTypes.SyncResponse(false, resp)
	}

	return mcTypes.SyncResponse(true, resp)
}

func usesDSLDiskAddRequest(req types.DisksPost) bool {
	return req.OSDMatch != "" || req.WALMatch != "" || req.DBMatch != "" || req.DryRun || req.WALSize != "" || req.DBSize != ""
}

func validatePositiveByteSizeString(value string, flagName string) error {
	sizeBytes, err := units.ParseByteSizeString(value)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", flagName, err)
	}
	if sizeBytes <= 0 {
		return fmt.Errorf("%s must be greater than 0", flagName)
	}
	return nil
}

func validateDiskPostRequest(req types.DisksPost) error {
	usesDSL := usesDSLDiskAddRequest(req)

	if usesDSL && len(req.Path) > 0 {
		return fmt.Errorf("--osd-match/--wal-match/--db-match cannot be used with positional device arguments")
	}

	if req.DryRun && req.OSDMatch == "" {
		return fmt.Errorf("--dry-run requires --osd-match")
	}

	if usesDSL && (req.WALDev != nil || req.DBDev != nil) {
		return fmt.Errorf("--wal-device and --db-device are not supported with DSL matching in this version")
	}

	if req.WALMatch != "" && req.OSDMatch == "" {
		return fmt.Errorf("--wal-match requires --osd-match")
	}
	if req.DBMatch != "" && req.OSDMatch == "" {
		return fmt.Errorf("--db-match requires --osd-match")
	}
	if req.WALMatch != "" && req.WALSize == "" {
		return fmt.Errorf("--wal-match requires --wal-size")
	}
	if req.DBMatch != "" && req.DBSize == "" {
		return fmt.Errorf("--db-match requires --db-size")
	}
	if req.WALSize != "" && req.WALMatch == "" {
		return fmt.Errorf("--wal-size requires --wal-match")
	}
	if req.DBSize != "" && req.DBMatch == "" {
		return fmt.Errorf("--db-size requires --db-match")
	}
	if req.WALEncrypt && req.WALMatch == "" && req.WALDev == nil {
		return fmt.Errorf("--wal-encrypt requires --wal-match or --wal-device")
	}
	if req.WALWipe && req.WALMatch == "" && req.WALDev == nil {
		return fmt.Errorf("--wal-wipe requires --wal-match or --wal-device")
	}
	if req.DBEncrypt && req.DBMatch == "" && req.DBDev == nil {
		return fmt.Errorf("--db-encrypt requires --db-match or --db-device")
	}
	if req.DBWipe && req.DBMatch == "" && req.DBDev == nil {
		return fmt.Errorf("--db-wipe requires --db-match or --db-device")
	}
	if req.WALMatch != "" {
		err := validatePositiveByteSizeString(req.WALSize, "--wal-size")
		if err != nil {
			return err
		}
	}
	if req.DBMatch != "" {
		err := validatePositiveByteSizeString(req.DBSize, "--db-size")
		if err != nil {
			return err
		}
	}

	return nil
}

// handleDSLDiskAdd handles DSL-based device selection for OSD creation and dry-run planning.
func handleDSLDiskAdd(r *http.Request, s mcTypes.State, req types.DisksPost) mcTypes.Response {
	resp := ceph.AddDisksWithDSLRequest(r.Context(), s, req)
	return mcTypes.SyncResponse(true, resp)
}

// cmdDisksDelete is the handler for DELETE /1.0/disks/{osdid}.
func cmdDisksDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var osd string
	osd, err := url.PathUnescape(mux.Vars(r)["osdid"])
	if err != nil {
		return mcTypes.BadRequest(err)
	}

	var req types.DisksDelete
	osdid, err := strconv.ParseInt(osd, 10, 64)

	if err != nil {
		return mcTypes.BadRequest(err)
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.BadRequest(err)
	}

	mu.Lock()
	defer mu.Unlock()

	cs := interfaces.CephState{State: s}

	// if check for crush rule scaledown only if crush change is not prohibited.
	if !req.ProhibitCrushScaledown {
		needDowngrade, err := ceph.IsDowngradeNeeded(r.Context(), cs, osdid)
		if err != nil {
			return mcTypes.InternalError(err)
		}
		if needDowngrade && !req.ConfirmDowngrade {
			errorMsg := fmt.Errorf(
				"removing osd.%s would require a downgrade of the automatic crush rule from 'host' to 'osd' level. "+
					"Likely this will result in additional data movement. Please confirm by setting the "+
					"'--confirm-failure-domain-downgrade' flag to true",
				osd,
			)
			return mcTypes.BadRequest(errorMsg)
		}
	}

	// Warn if --confirm-failure-domain-downgrade is set but the cluster uses
	// rack-level failure domain. Downgrading from rack is not supported —
	// the flag has no effect in this case. Errors are non-fatal here since
	// this is only an advisory warning.
	if req.ConfirmDowngrade {
		onRack, err := ceph.IsOnRackRule()
		if err != nil {
			logger.Warnf("Could not determine crush rule type: %v", err)
		} else if onRack {
			logger.Warnf(
				"--confirm-failure-domain-downgrade has no effect: cluster uses rack-level " +
					"failure domain (availability zones). Downgrade from rack is not supported.",
			)
		}
	}

	// Block removal if it would break rack-level failure domain (AZ topology).
	if !req.BypassSafety {
		blocked, err := ceph.IsRackDegradeBlocked(r.Context(), cs, osdid)
		if err != nil {
			return mcTypes.InternalError(err)
		}
		if blocked {
			return mcTypes.BadRequest(fmt.Errorf(
				"removing osd.%s would leave fewer than 3 availability zones with OSDs "+
					"while the cluster uses rack-level failure domain, which would make "+
					"data placement unsatisfiable. Use --bypass-safety-checks to override",
				osd,
			))
		}
	}

	err = ceph.RemoveOSD(r.Context(), cs, osdid, req.BypassSafety, req.Timeout)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
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
	if diskPath.IsArray() {
		// use unpatched buffer if client is using Batch Disk params.
		patchedBody = buf
	} else if diskPath.Exists() && diskPath.String() != "" {
		patchedBody, err = sjson.Set(buf, "path", []string{diskPath.String()})
		if err != nil {
			return types.DisksPost{}, err
		}
	} else {
		// Empty or nonexistent path, return empty
		patchedBody, err = sjson.Set(buf, "path", []string{})
		if err != nil {
			return types.DisksPost{}, err
		}
	}

	logger.Debugf("CmdDiskPost Patched Body: %v", patchedBody)

	err = json.Unmarshal([]byte(patchedBody), &output)
	if err != nil {
		return types.DisksPost{}, err
	}

	return output, nil
}
