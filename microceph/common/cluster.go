package common

import "github.com/canonical/lxd/shared/logger"

func GetClusterMemberNames(s StateInterface) ([]string, error) {
	leader, err := s.ClusterState().Leader()
	if err != nil {
		return nil, err
	}

	members, err := leader.GetClusterMembers(s.ClusterState().Context)
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
