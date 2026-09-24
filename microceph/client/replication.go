package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
)

const (
	// replicationReadTimeout bounds the list and status replication requests.
	replicationReadTimeout = 120 * time.Second
	// replicationMutateTimeout bounds the requests that change state. The daemon
	// handles them synchronously and may retry for several minutes (see
	// ceph.DisablePoolMirroring), so the client must wait longer than that.
	replicationMutateTimeout = 600 * time.Second
)

// replicationTimeout returns the client timeout for a replication request of the given
// event type (see constants.Event*Replication).
func replicationTimeout(eventType string) time.Duration {
	switch eventType {
	case constants.EventListReplication, constants.EventStatusReplication:
		return replicationReadTimeout
	default:
		return replicationMutateTimeout
	}
}

// SendReplicationRequest sends replication request for creating, deleting, getting, and listing remote replication.
func SendReplicationRequest(ctx context.Context, c *microCli.Client, data types.ReplicationRequest) (string, error) {
	var err error
	var resp string
	queryCtx, cancel := context.WithTimeout(ctx, replicationTimeout(data.GetWorkloadRequestType()))
	defer cancel()

	// If no API object provided, create API request to the root endpoint.
	if len(data.GetAPIObjectID()) == 0 {
		// uses replication/$workload endpoint
		err = c.Query(
			queryCtx, data.GetAPIRequestType(), types.ExtendedPathPrefix,
			api.NewURL().Path("ops", "replication", string(data.GetWorkloadType())),
			data, &resp,
		)
	} else {
		// Other requests use replication/$workload/$resource endpoint
		err = c.Query(
			queryCtx, data.GetAPIRequestType(), types.ExtendedPathPrefix,
			api.NewURL().Path("ops", "replication", string(data.GetWorkloadType()), data.GetAPIObjectID()),
			data, &resp,
		)
	}
	if err != nil {
		return "", fmt.Errorf("failed to process %s request for %s: %w", data.GetWorkloadRequestType(), data.GetWorkloadType(), err)
	}

	return resp, nil
}
