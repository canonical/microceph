package common

import (
	"context"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/interfaces"
)

func GetClusterMemberNames(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	leader, err := s.ClusterState().Leader()
	if err != nil {
		return nil, err
	}

	members, err := leader.GetClusterMembers(ctx)
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
