package ceph

import (
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	microCli "github.com/canonical/microcluster/client"

	"github.com/canonical/microceph/microceph/client"
)

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

	// delete from cluster db
	err = client.MClient.DeleteClusterMember(cli, node, force)
	logger.Debugf("DeleteClusterMember %v: %v", node, err)
	if err != nil {
		return err
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
	// create a map of service names counters
	// init with false
	foundMap := map[string]int{
		"mon": 0,
		"mgr": 0,
		"mds": 0,
	}
	// loop through services and check service counts
	for _, service := range services {
		if service.Location == name {
			continue
		}
		foundMap[service.Service]++
	}
	logger.Debugf("Services: %v, foundMap: %v", services, foundMap)
	if foundMap["mon"] < 3 || foundMap["mgr"] < 1 || foundMap["mds"] < 1 {
		return fmt.Errorf("Need at least 3 mon, 1 mds, and 1 mgr besides %v", name)
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
