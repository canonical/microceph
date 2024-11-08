package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/v2/client"
)

// Sends replication request for creating, deleting, getting, and listing remote replication.
func SendReplicationRequest(ctx context.Context, c *microCli.Client, data types.ReplicationRequest) (string, error) {
	var err error
	var resp string
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// If no API object provided, create API request to the root endpoint.
	if len(data.GetAPIObjectId()) == 0 {
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
			api.NewURL().Path("ops", "replication", string(data.GetWorkloadType()), data.GetAPIObjectId()),
			data, &resp,
		)
	}
	if err != nil {
		return "", fmt.Errorf("failed to process %s request for %s: %w", data.GetWorkloadRequestType(), data.GetWorkloadType(), err)
	}

	return resp, nil
}
