package client

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
)

// serverDisableRetryBudget is the worst case time the daemon sleeps between retries in
// ceph.DisablePoolMirroring: 5s+40s+40s for the local pool disable, then a 5s delay and
// a 5s linear backoff over 10 attempts (5s*(1+...+9)) for the remote one.
const serverDisableRetryBudget = (5+40+40)*time.Second + (5+5*45)*time.Second

func TestReplicationTimeout(t *testing.T) {
	// Requests that only read keep the short timeout.
	assert.Equal(t, replicationReadTimeout, replicationTimeout(constants.EventListReplication))
	assert.Equal(t, replicationReadTimeout, replicationTimeout(constants.EventStatusReplication))

	// Everything else runs the replication state machine synchronously.
	for _, event := range []string{
		constants.EventEnableReplication,
		constants.EventDisableReplication,
		constants.EventConfigureReplication,
		constants.EventPromoteReplication,
		constants.EventDemoteReplication,
		"",
	} {
		assert.Equal(t, replicationMutateTimeout, replicationTimeout(event), event)
	}

	// The mutate timeout must outlast the server side retries with room for the commands.
	assert.Greater(t, replicationMutateTimeout, serverDisableRetryBudget+120*time.Second)
}

func TestReplicationTimeoutFromRequest(t *testing.T) {
	disable := types.RbdReplicationRequest{RequestType: types.DisableReplicationRequest}
	assert.Equal(t, replicationMutateTimeout, replicationTimeout(disable.GetWorkloadRequestType()))

	list := types.RbdReplicationRequest{RequestType: types.ListReplicationRequest}
	assert.Equal(t, replicationReadTimeout, replicationTimeout(list.GetWorkloadRequestType()))
}
