package ceph

import (
	"fmt"

	"github.com/canonical/lxd/shared/logger"

	microCli "github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/client"
)

// EnterMaintenance put a given node into maintanence mode.
func EnterMaintenance(clusterClient *microCli.Client, cephClient client.ClientInterface, name string, force, dryRun, setNoout, stopOsds bool) error {
	ops := []operation{}

	// pre-flight checks
	if !force {
		ops = append(ops, []operation{
			&checkNodeInClusterOps{cephClient, clusterClient},
			&checkOsdOkToStopOps{cephClient, clusterClient},
			&checkNonOsdSvcEnoughOps{cephClient, clusterClient, 3, 1, 1},
		}...)
	}

	// optionally set noout
	if setNoout {
		ops = append(ops, []operation{
			&setNooutOps{},
			&assertNooutFlagSetOps{},
		}...)
	}

	// optionally stop osd service
	if stopOsds {
		ops = append(ops, []operation{
			&stopOsdOps{cephClient, clusterClient},
		}...)
	}

	m := maintenance{name}
	err := m.Run(ops, dryRun)
	if err != nil {
		return fmt.Errorf("Failed to enter maintenance mode: %v", err)
	}
	return nil
}

// ExitMaintenance recover a given node from maintanence mode.
func ExitMaintenance(clusterClient *microCli.Client, cephClient client.ClientInterface, name string, dryRun bool) error {
	ops := []operation{}

	// preflight checks
	ops = append(ops, []operation{
		&checkNodeInClusterOps{cephClient, clusterClient},
	}...)

	// idempotently unset noout and start osd service
	ops = append(ops, []operation{
		&unsetNooutOps{},
		&assertNooutFlagUnsetOps{},
		&startOsdOps{cephClient, clusterClient},
	}...)

	m := maintenance{name}
	err := m.Run(ops, dryRun)
	if err != nil {
		return fmt.Errorf("Failed to exit maintenance mode: %v", err)
	}
	return nil
}

type maintenance struct {
	nodeName string
}

func (m *maintenance) Run(operations []operation, dryRun bool) error {
	for _, ops := range operations {
		if dryRun {
			fmt.Println(ops.DryRun(m.nodeName))
		} else {
			err := ops.Run(m.nodeName)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

//
// operations
//

type operation interface {
	Run(string) error
	DryRun(string) string
}

type checkNodeInClusterOps struct {
	cephClient    client.ClientInterface
	clusterClient *microCli.Client
}

func (o *checkNodeInClusterOps) Run(name string) error {
	clusterMembers, err := o.cephClient.GetClusterMembers(o.clusterClient)
	if err != nil {
		return fmt.Errorf("Error getting cluster members: %v", err)
	}

	for _, member := range clusterMembers {
		if member == name {
			logger.Infof("Node '%s' is in the cluster.", name)
			return nil
		}
	}

	return fmt.Errorf("Node '%s' not found", name)
}

func (o *checkNodeInClusterOps) DryRun(name string) string {
	return fmt.Sprintf("Check if node '%s' is in the cluster.", name)
}

type checkOsdOkToStopOps struct {
	cephClient    client.ClientInterface
	clusterClient *microCli.Client
}

func (o *checkOsdOkToStopOps) Run(name string) error {
	disks, err := o.cephClient.GetDisks(o.clusterClient)
	if err != nil {
		return fmt.Errorf("Error getting disks: %v", err)
	}

	OsdsToCheck := []int64{}
	for _, disk := range disks {
		if disk.Location == name {
			OsdsToCheck = append(OsdsToCheck, disk.OSD)
		}
	}

	if !testSafeStop(OsdsToCheck) {
		return fmt.Errorf("osd.%v cannot be safely stopped", OsdsToCheck)
	}

	logger.Infof("osd.%v can be safely stopped.", OsdsToCheck)
	return nil
}

func (o *checkOsdOkToStopOps) DryRun(name string) string {
	return fmt.Sprintf("Check if osds in node '%s' are ok-to-stop.", name)
}

type checkNonOsdSvcEnoughOps struct {
	cephClient    client.ClientInterface
	clusterClient *microCli.Client

	minMon int
	minMds int
	minMgr int
}

func (o *checkNonOsdSvcEnoughOps) Run(name string) error {
	services, err := o.cephClient.GetServices(o.clusterClient)
	if err != nil {
		return fmt.Errorf("Error getting services: %v", err)
	}

	remains := map[string]int{
		"mon": 0,
		"mgr": 0,
		"mds": 0,
	}
	for _, service := range services {
		// do not count the service on this node
		if service.Location != name {
			remains[service.Service]++
		}
	}

	// the remaining services must be sufficient to make the cluster healthy after the node enters
	// maintanence mode.
	if remains["mon"] < o.minMon || remains["mds"] < o.minMds || remains["mgr"] < o.minMgr {
		return fmt.Errorf("Need at least %d mon, %d mds, and %d mgr services in the cluster besides those in node '%s'", o.minMon, o.minMds, o.minMgr, name)
	}
	logger.Infof("Remaining mon (%d), mds (%d), and mgr (%d) services in the cluster are enough after '%s' enters maintenance mode", remains["mon"], remains["mds"], remains["mgr"], name)

	return nil
}

func (o *checkNonOsdSvcEnoughOps) DryRun(name string) string {
	return fmt.Sprintf("Check if there are at least %d mon, %d mds, and %d mgr services in the cluster besides those in node '%s'", o.minMon, o.minMds, o.minMgr, name)
}

type setNooutOps struct{}

func (o *setNooutOps) Run(name string) error {
	err := osdNooutFlag(true)
	if err != nil {
		return err
	}
	return nil
}

func (o *setNooutOps) DryRun(name string) string {
	return fmt.Sprint("Run `ceph osd set noout`.")
}

type assertNooutFlagSetOps struct{}

func (o *assertNooutFlagSetOps) Run(name string) error {
	set, err := isOsdNooutSet()
	if err != nil {
		return err
	}
	if !set {
		return fmt.Errorf("OSD has 'noout' flag unset.")
	}
	logger.Info("OSD has 'noout' flag set.")
	return nil
}

func (o *assertNooutFlagSetOps) DryRun(name string) string {
	return fmt.Sprint("Assert OSD has 'noout' flag set.")
}

type assertNooutFlagUnsetOps struct{}

func (o *assertNooutFlagUnsetOps) Run(name string) error {
	set, err := isOsdNooutSet()
	if err != nil {
		return err
	}
	if set {
		return fmt.Errorf("OSD has 'noout' flag set.")
	}
	logger.Info("OSD has 'noout' flag unset.")
	return nil
}

func (o *assertNooutFlagUnsetOps) DryRun(name string) string {
	return fmt.Sprint("Assert OSD has 'noout' flag unset.")
}

type stopOsdOps struct {
	cephClient    client.ClientInterface
	clusterClient *microCli.Client
}

func (o *stopOsdOps) Run(name string) error {
	err := o.cephClient.PutOsds(o.clusterClient, false, name)
	if err != nil {
		logger.Errorf("Unable to stop OSD service in node '%s'.", name)
		return err
	}
	logger.Infof("Stopped OSD service in node '%s'.", name)
	return nil
}

func (o *stopOsdOps) DryRun(name string) string {
	return fmt.Sprintf("Stop OSD service in node '%s'.", name)
}

type startOsdOps struct {
	cephClient    client.ClientInterface
	clusterClient *microCli.Client
}

func (o *startOsdOps) Run(name string) error {
	err := o.cephClient.PutOsds(o.clusterClient, true, name)
	if err != nil {
		logger.Errorf("Unable to start OSD service in node '%s'.", name)
		return err
	}
	logger.Infof("Started OSD service in node '%s'", name)
	return nil
}

func (o *startOsdOps) DryRun(name string) string {
	return fmt.Sprintf("Start osd services in node '%s'.", name)
}

type unsetNooutOps struct{}

func (o *unsetNooutOps) Run(name string) error {
	err := osdNooutFlag(false)
	if err != nil {
		return err
	}
	logger.Info("Unset osd noout.")
	return nil
}

func (o *unsetNooutOps) DryRun(name string) string {
	return fmt.Sprint("Run `ceph osd unset noout`.")
}
