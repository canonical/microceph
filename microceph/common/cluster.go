package common

import (
	"context"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

func GetClusterMemberNames(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	leader, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return nil, err
	}

	var members []mcTypes.ClusterMember
	err = leader.Query(ctx, "GET", mcTypes.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &members)
	if err != nil {
		return nil, err
	}

	logger.Infof("Cluster Members are: %v", members)

	memberNames := make([]string, len(members))
	for i, member := range members {
		memberNames[i] = member.Name
	}

	logger.Infof("Cluster Members Names are: %v", memberNames)

	return memberNames, nil
}
