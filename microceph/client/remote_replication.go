package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/client"
)

// Sends replication request for creating, deleting, getting, and listing remote replication.
func SendRemoteReplicationRequest(ctx context.Context, c *microCli.Client, data types.ReplicationRequest) error {
	var err error
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	if data.GetRequestType() == types.ListReplicationRequest {
		err = c.Query(
			queryCtx, "GET", types.ExtendedPathPrefix,
			api.NewURL().Path("ops", "replication", string(data.GetCephWorkloadType())),
			data, nil,
		)
	} else {
		err = c.Query(
			queryCtx, string(data.GetRequestType()), types.ExtendedPathPrefix,
			api.NewURL().Path("ops", "replication", string(data.GetCephWorkloadType()), data.GetAPIObjectId()),
			data, nil,
		)
	}
	if err != nil {
		return fmt.Errorf("failed to process %s request for %s: %w", data.GetRequestType(), data.GetCephWorkloadType(), err)
	}

	return nil
}
