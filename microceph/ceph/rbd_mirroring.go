package ceph

import (
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
)

func EnableRbdReplication(data types.RbdReplicationRequest) error {
	logger.Errorf("BAZINGA %v", data)
	// TODO: implement
	return nil
}
