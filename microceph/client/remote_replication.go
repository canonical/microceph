package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	microCli "github.com/canonical/microcluster/client"
)

// Sends replication request for creating, deleting, getting, and listing remote replication.
func SendRemoteReplicationRequest(ctx context.Context, c *microCli.Client, data types.ReplicationRequest) (string, error) {
	var err error
	var resp string
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	if data.GetWorkloadRequestType() == constants.EventListReplication {
		// list request uses replication/$workload endpoint
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
