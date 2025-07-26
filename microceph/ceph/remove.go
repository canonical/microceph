package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	microCli "github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

// PreRemove cleans up the underlying ceph services before the node is removed from the dqlite cluster.
func PreRemove(m *microcluster.MicroCluster) func(ctx context.Context, s state.State, force bool) error {
	return func(ctx context.Context, s state.State, force bool) error {
		cli, err := m.LocalClient()
		if err != nil {
			return err
		}

		return removeNode(cli, s.Name(), force)
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

func removeNode(cli *microCli.Client, node string, force bool) error {

	logger.Debugf("Removing cluster member %v, force: %v", node, force)

	// check prerquisites unless we're forcing
	if !force {
		err := checkPrerequisites(cli, node)
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

func checkPrerequisites(cli *microCli.Client, name string) error {
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

func deleteNodeServices(cli *microCli.Client, name string) error {
	services, err := client.MClient.GetServices(cli)
	if err != nil {
		return err
	}
	for _, service := range services {
		logger.Debugf("Check for deletion: %s", service)
		if service.Location == name {
			logger.Debugf("Deleting service %s", service)
			err = client.MClient.DeleteService(cli, service.Location, service.Service)
			if err != nil {
				logger.Warnf("Fault deleting service %v on node %v: %v", service.Service, service.Location, err)
			}
		}
	}
	return nil

}
