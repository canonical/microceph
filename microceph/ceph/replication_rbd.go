package ceph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
)

type RbdReplicationPeer struct {
	LocalId    string
	RemoteId   string
	RemoteName string
	Direction  types.RbdReplicationDirection
}

type RbdReplicationPoolInfo struct {
	Mode          types.RbdResourceType
	LocalSiteName string
	Peers         []RbdReplicationPeer
}

type RbdReplicationHealth string

const (
	RbdReplicationHealthOK   RbdReplicationHealth = "OK"
	RbdReplicationHealthWarn RbdReplicationHealth = "WARNING"
	RbdReplicationHealthErr  RbdReplicationHealth = "Error"
)

type RbdReplicationPoolStatus struct {
	State        ReplicationState
	Health       RbdReplicationHealth
	DaemonHealth RbdReplicationHealth
	ImageHealth  RbdReplicationHealth
	ImageCount   int
}

type RbdReplicationImageStatus struct {
	ID         string
	State      ReplicationState
	Status     string
	isPrimary  bool
	LastUpdate string
	Peers      []string
}

type RbdReplicationHandler struct {
	// Resource Info
	PoolInfo    RbdReplicationPoolInfo
	PoolStatus  RbdReplicationPoolStatus
	ImageStatus RbdReplicationImageStatus
	// Request Info
	Request types.RbdReplicationRequest
}

// PreFill populates the handler struct with requested rbd pool/image information.
func (rh *RbdReplicationHandler) PreFill(ctx context.Context, request types.ReplicationRequest) error {
	var err error
	req := request.(types.RbdReplicationRequest)
	rh.Request = req
	// Populate pool Info
	rh.PoolInfo, err = GetRbdMirrorPoolInfo(req.SourcePool, "", "")
	if err != nil {
		return err
	}

	// Populate pool status
	rh.PoolStatus, err = GetRbdMirrorPoolStatus(req.SourcePool, "", "")
	if err != nil {
		return err
	}

	if req.ResourceType == types.RbdResourceImage {
		// Populate image status
		rh.ImageStatus, err = GetRbdMirrorImageStatus(req.SourcePool, req.SourceImage, "", "")
		return err
	}

	return nil
}

// GetResourceState fetches the mirroring state for requested rbd pool/image.
func (rh *RbdReplicationHandler) GetResourceState() ReplicationState {
	// Image request but mirroring is disabled on image.
	if rh.Request.ResourceType == types.RbdResourceImage {
		return rh.ImageStatus.State
	}

	// Pool request
	return rh.PoolStatus.State
}

// EnableHandler enables mirroring for requested rbd pool/image.
func (rh *RbdReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	// TODO: remove
	var localName string
	var remoteName string
	if rh.Request.RemoteName == "magical" {
		localName = "simple"
		remoteName = "magical"
	} else {
		localName = "magical"
		remoteName = "simple"
	}

	logger.Infof("BAZINGA: Entered RBD Enable Handler R%s L%s", remoteName, localName)
	if rh.Request.ResourceType == types.RbdResourcePool {
		if rh.PoolStatus.State == StateDisabledReplication {
			return EnablePoolMirroring(rh.Request.SourcePool, types.RbdResourcePool, localName, remoteName)
		} else {
			return fmt.Errorf("pool already enabled in %s mirroring mode", rh.PoolInfo.Mode)
		}
	} else if rh.Request.ResourceType == types.RbdResourceImage {
		if rh.ImageStatus.State == StateDisabledReplication {
			if rh.PoolStatus.State == StateDisabledReplication {
				return fmt.Errorf("parent pool(%s) is disabled", rh.Request.SourcePool)
			}
			// TODO: remove hardcoding for journaling
			return EnableImageMirroring(rh.Request.SourcePool, rh.Request.SourceImage, types.RbdReplicationJournaling, localName, remoteName)
		} else {
			return fmt.Errorf("image already enabled for mirroring")
		}
	}

	return fmt.Errorf("unknown request for rbd mirroring %s", rh.Request.ResourceType)
}

// DisableHandler disables mirroring configured for requested rbd pool/image.
func (rh *RbdReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	return nil
}

// ConfigureHandler configures replication properties for requested rbd pool/image.
func (rh *RbdReplicationHandler) ConfigureHandler(ctx context.Context, args ...any) error { return nil }

// ListHandler fetches a list of rbd pools/images configured for mirroring.
func (rh *RbdReplicationHandler) ListHandler(ctx context.Context, args ...any) error { return nil }

// StatusHandler fetches the status of requested rbd pool/image resource.
func (rh *RbdReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	data, err := json.Marshal(rh)
	if err != nil {
		err := fmt.Errorf("failed to marshal resource status: %w", err)
		logger.Error(err.Error())
		return err
	}

	// pass resoponse back to API
	*args[1].(*string) = string(data)
	return nil
}
