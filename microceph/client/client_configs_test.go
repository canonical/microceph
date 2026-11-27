package client

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/mocks"
)

func TestSendUpdateClientConfRequestToClusterMembers(t *testing.T) {
	origUpdate := updateClientConfFunc
	defer func() { updateClientConfFunc = origUpdate }()

	si := mocks.NewStateInterface(t)
	connector := mocks.NewConnectorInterface(t)
	si.On("ClusterState").Return(&mocks.MockState{ConnectorObj: connector}).Maybe()
	connector.On("Cluster", false).Return(mcTypes.Clients{nil, nil}, nil).Maybe()

	calls := 0
	updateClientConfFunc = func(ctx context.Context, c mcTypes.Client) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("member one down")
		}
		return nil
	}

	// One member fails but the other is still updated, and the aggregated
	// error names the failures: the first-failure return used to leave the
	// remaining members stale.
	err := SendUpdateClientConfRequestToClusterMembers(context.Background(), si)
	require.Error(t, err)
	assert.Equal(t, 2, calls)
	assert.Contains(t, err.Error(), "1 member(s)")
	assert.Contains(t, err.Error(), "member one down")

	// All members succeed: no error.
	updateClientConfFunc = func(ctx context.Context, c mcTypes.Client) error {
		calls++
		return nil
	}
	err = SendUpdateClientConfRequestToClusterMembers(context.Background(), si)
	require.NoError(t, err)
	assert.Equal(t, 4, calls)
}
