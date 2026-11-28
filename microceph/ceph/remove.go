package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// PreRemove cleans up the underlying ceph services before the node is removed from the dqlite cluster.
func PreRemove(m *microcluster.MicroCluster) func(ctx context.Context, s mcTypes.State, force bool) error {
	logger.Debug("Setting up PreRemove hook")
	return func(ctx context.Context, s mcTypes.State, force bool) error {
		logger.Infof("Executing PreRemove hook for node %s (force=%v)", s.Name(), force)
		cli, err := m.LocalClient()
		if err != nil {
			return err
		}

		err = removeNode(cli, s.Name(), force, func() error {
			// Keep identity/state until the exact local owner has stopped.
			return retireSMBForRemoval(ctx, interfaces.CephState{State: s}, force)
		})
		if err != nil {
			return err
		}

		return nil
	}
}

// EnsureNonOsdSvcEnough ensures non OSD services beside those in nodeName are enough to maintain a healthy cluster.
func EnsureNonOsdSvcEnough(services types.Services, nodeName string, minMon int, minMgr int, minMds int) error {
	// remaining non osd services maps
	foundMap := map[string]int{
		"mon": 0,
		"mgr": 0,
		"mds": 0,
	}

	// loop through services and check service counts not on the node 'nodeName'
	for _, service := range services {
		if service.Location != nodeName {
			foundMap[service.Service]++
		}
	}

	logger.Debugf("Services: %v, foundMap: %v", services, foundMap)
	if foundMap["mon"] < minMon || foundMap["mgr"] < minMgr || foundMap["mds"] < minMds {
		return fmt.Errorf("need at least %d mon, %d mds, and %d mgr services in the cluster besides those in node '%s'", minMon, minMds, minMgr, nodeName)
	}

	return nil
}

func removeNode(cli mcTypes.Client, node string, force bool, beforeDeletion ...func() error) error {
	logger.Debugf("Removing cluster member %v, force: %v", node, force)

	// check prerquisites unless we're forcing
	if !force {
		err := checkPrerequisites(cli, node)
		if err != nil {
			return err
		}
	}

	// PreRemove attaches native retirement after prerequisites, but before
	// deleting success rows or letting MicroCluster discard core identity.
	for _, retire := range beforeDeletion {
		err := retire()
		if err != nil {
			return err
		}
	}

	// delete from ceph
	err := deleteNodeServices(cli, node)
	if err != nil {
		// forcing makes errs non-fatal
		if !force {
			return err
		}
		logger.Warnf("Error deleting services from node %v: %v", node, err)
	}

	return nil
}

func checkPrerequisites(cli mcTypes.Client, name string) error {
	// check if member exists
	clusterMembers, err := client.MClient.GetClusterMembers(cli)
	if err != nil {
		return fmt.Errorf("Error getting cluster members: %v", err)
	}
	found := false
	for _, member := range clusterMembers {
		if member == name {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("Node %v not found", name)
	}

	// check if any OSDs present
	disks, err := client.MClient.GetDisks(cli)
	if err != nil {
		return fmt.Errorf("Error getting disks: %v", err)
	}
	found = false
	for _, disk := range disks {
		if disk.Location == name {
			found = true
		}
	}
	logger.Debugf("Disks: %v, found: %v", disks, found)
	if found {
		return fmt.Errorf("Node %v still has disks configured, remove before proceeding", name)
	}

	// check if this node has the last mon
	services, err := client.MClient.GetServices(cli)
	if err != nil {
		return fmt.Errorf("Error getting services: %v", err)
	}

	// check if the remaining non OSD services is enough to maintain a healthy cluster
	err = EnsureNonOsdSvcEnough(services, name, 3, 1, 1)
	if err != nil {
		return fmt.Errorf("Insufficient non OSD services: %v", err)
	}

	return nil
}

func deleteNodeServices(cli mcTypes.Client, name string) error {
	services, err := client.MClient.GetServices(cli)
	if err != nil {
		return err
	}

	deleteErrors := []error{}
	for _, service := range services {
		logger.Debugf("Check for deletion: %s", service)
		if service.Location == name {
			logger.Debugf("Deleting service %s", service)
			if service.Service == "smb" {
				err = deleteGroupedSMBService(context.Background(), cli, service)
			} else {
				err = client.MClient.DeleteService(cli, service.Location, service.Service)
			}
			if err != nil {
				logger.Warnf("Fault deleting service %v on node %v: %v", service.Service, service.Location, err)
				deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete service %q on node %q: %w", service.Service, service.Location, err))
			}
		}
	}

	return errors.Join(deleteErrors...)
}

var deleteSMBServiceFunc = client.DeleteSMBService

func deleteGroupedSMBService(ctx context.Context, cli mcTypes.Client, service types.Service) error {
	if service.GroupID == "" {
		return fmt.Errorf("SMB removal requires the exact cluster ID; retain ownership uncertainty")
	}
	return deleteSMBServiceFunc(ctx, cli, service.Location, &types.SMBService{ClusterID: service.GroupID})
}

// retireSMBForRemoval uses the same explicit local disable lifecycle. Offline
// forced removal bypasses PreRemove in MicroCluster; JSON ownership survives
// core-member deletion. Force here suppresses errors, never certifies a stop.
func retireSMBForRemoval(ctx context.Context, s interfaces.StateInterface, force bool) error {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	var groups []string
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		groups, err = database.GetSMBOwnerGroups(ctx, tx, s.ClusterState().Name())
		return err
	})
	if err == nil {
		if len(groups) > 1 {
			err = fmt.Errorf("conflicting SMB owner groups; retain uncertainty")
		} else if len(groups) == 1 {
			err = disableSMBLocked(ctx, s, groups[0], false)
		}
	}
	if err != nil && force {
		logger.Warn("Forced removal cannot confirm SMB retirement; retained owner must not be reused")
		return nil
	}
	return err
}
