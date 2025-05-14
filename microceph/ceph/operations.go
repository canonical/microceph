package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/v2/state"
)

// RunOperations runs the provided operations or return the action plan.
func RunOperations(name string, operations []Operation, dryRun, force bool) []Result {
	results := []Result{}

	for _, op := range operations {
		result := Result{Name: op.GetName(), Error: "", Action: op.DryRun(name)}
		if dryRun {
			results = append(results, result)
		} else {
			err := op.Run(name)
			if err != nil {
				logger.Errorf("%v", err)
				result.Error = fmt.Sprintf("%v", err)
				results = append(results, result)
				if force {
					logger.Warnf("ignored '%v' because it's forced.", err)
					continue
				}
				return results
			} else {
				results = append(results, result)
			}
		}
	}

	return results
}

// Operation is a interface for ceph and microceph operations.
type Operation interface {
	// Run executes the operation and return the error if any.
	Run(string) error

	// DryRun returns the string representation of the operation.
	DryRun(string) string

	// GetName returns the name of the operation.
	GetName() string
}

type Result struct {
	Name   string `json:"name"`
	Error  string `json:"error"`
	Action string `json:"action"`
}

// ClusterOps is the base struct for all operations.
type ClusterOps struct {
	State   state.State
	Context context.Context
}

// CheckOsdOkToStopOps is an operation to check if osds in a node are ok-to-stop.
type CheckOsdOkToStopOps struct {
	ClusterOps
}

// Run checks osds in a node are ok-to-stop.
func (o *CheckOsdOkToStopOps) Run(name string) error {
	OsdsToCheck, err := o.getOsds(name)
	if err != nil {
		return err
	}

	if !testSafeStop(OsdsToCheck) {
		return fmt.Errorf("osds.%v cannot be safely stopped", OsdsToCheck)
	}

	logger.Infof("osds.%v can be safely stopped.", OsdsToCheck)
	return nil
}

// DryRun prints out the action plan.
func (o *CheckOsdOkToStopOps) DryRun(name string) string {
	osdsToCheck, _ := o.getOsds(name)
	return fmt.Sprintf("Check if osds.%v in node '%s' are ok-to-stop.", osdsToCheck, name)
}

func (o *CheckOsdOkToStopOps) getOsds(name string) ([]int64, error) {
	disks, err := ListOSD(o.Context, o.State)
	if err != nil {
		return []int64{}, fmt.Errorf("error listing disks: %v", err)
	}

	OsdsToCheck := []int64{}
	for _, disk := range disks {
		if disk.Location == name {
			OsdsToCheck = append(OsdsToCheck, disk.OSD)
		}
	}
	return OsdsToCheck, nil
}

// GetName returns the name of the action
func (o *CheckOsdOkToStopOps) GetName() string {
	return "check-osd-ok-to-stop-ops"
}

// CheckNonOsdSvcEnoughOps is an operation to check if non-osd service in a node are enough.
type CheckNonOsdSvcEnoughOps struct {
	ClusterOps
}

// Run checks if non-osds service in a node are enough.
func (o *CheckNonOsdSvcEnoughOps) Run(name string) error {
	services, err := ListServices(o.Context, o.State)
	if err != nil {
		return fmt.Errorf("error listing services: %v", err)
	}

	remains := map[string]int{
		"mon": 0,
		"mgr": 0,
		"mds": 0,
	}
	totals := map[string]int{
		"mon": 0,
		"mgr": 0,
		"mds": 0,
	}
	for _, service := range services {
		// do not count the service on this node
		if service.Location != name {
			remains[service.Service]++
		}
		totals[service.Service]++
	}

	// a majority of ceph-mon services must remain active to retain quorum
	minMon := totals["mon"] / 2 + 1
	// only need one ceph-mds and one ceph-mgr: they operate as one active, the rest in standby
	minMds := 1
	minMgr := 1

	// the remaining services must be sufficient to make the cluster healthy after the node enters
	// maintanence mode.
	if remains["mon"] < minMon || remains["mds"] < minMds || remains["mgr"] < minMgr {
		return fmt.Errorf("need at least %d mon, %d mds, and %d mgr services in the cluster besides those in node '%s'", minMon, minMds, minMgr, name)
	}
	logger.Infof("remaining mon (%d), mds (%d), and mgr (%d) services in the cluster are enough after '%s' enters maintenance mode", remains["mon"], remains["mds"], remains["mgr"], name)

	return nil
}

// DryRun prints out the action plan.
func (o *CheckNonOsdSvcEnoughOps) DryRun(name string) string {
	return fmt.Sprintf("Check if there are at least a majority of mon services, 1 mds service, and 1 mgr service in the cluster besides those in node '%s'", name)
}

// GetName returns the name of the action
func (o *CheckNonOsdSvcEnoughOps) GetName() string {
	return "check-non-osd-svc-enough-ops"
}

// SetNooutOps is an operation to set noout for the ceph cluster.
type SetNooutOps struct {
	ClusterOps
}

// Run `ceph osd set noout` for the ceph cluster.
func (o *SetNooutOps) Run(name string) error {
	err := setOsdNooutFlag(true)
	if err != nil {
		return err
	}
	return nil
}

// DryRun prints out the action plan.
func (o *SetNooutOps) DryRun(name string) string {
	return "Run `ceph osd set noout`."
}

// GetName returns the name of the action
func (o *SetNooutOps) GetName() string {
	return "set-noout-ops"
}

// AssertNooutFlagSetOps is an operation to assert noout has been set for the ceph cluster.
type AssertNooutFlagSetOps struct {
	ClusterOps
}

// Run asserts noout has been set for the ceph cluster.
func (o *AssertNooutFlagSetOps) Run(name string) error {
	set, err := isOsdNooutSet()
	if err != nil {
		return err
	}
	if !set {
		return fmt.Errorf("osd has 'noout' flag unset.")
	}
	logger.Info("osd has 'noout' flag set.")
	return nil
}

// DryRun prints out the action plan.
func (o *AssertNooutFlagSetOps) DryRun(name string) string {
	return "Assert osd has 'noout' flag set."
}

// GetName returns the name of the action
func (o *AssertNooutFlagSetOps) GetName() string {
	return "assert-noout-flag-set-ops"
}

// AssertNooutFlagUnsetOps is an operation to assert noout has been unset for the ceph cluster.
type AssertNooutFlagUnsetOps struct {
	ClusterOps
}

// Run asserts noout has been unset for the ceph cluster.
func (o *AssertNooutFlagUnsetOps) Run(name string) error {
	set, err := isOsdNooutSet()
	if err != nil {
		return err
	}
	if set {
		return fmt.Errorf("osd has 'noout' flag set.")
	}
	logger.Info("osd has 'noout' flag unset.")
	return nil
}

// GetName returns the name of the action
func (o *AssertNooutFlagUnsetOps) GetName() string {
	return "assert-noout-flag-unset-ops"
}

// DryRun prints out the action plan.
func (o *AssertNooutFlagUnsetOps) DryRun(name string) string {
	return "Assert osd has 'noout' flag unset."
}

// StopOsdOps is an operation to stop osd service for a node.
type StopOsdOps struct {
	ClusterOps
}

// Run stops the osd service for a node.
func (o *StopOsdOps) Run(name string) error {
	err := SetOsdState(false)
	if err != nil {
		logger.Errorf("unable to stop osd service in node '%s': %v", name, err)
		return err
	}
	logger.Infof("stopped osd service in node '%s'.", name)
	return nil
}

// DryRun prints out the action plan.
func (o *StopOsdOps) DryRun(name string) string {
	return fmt.Sprintf("Stop osd service in node '%s'.", name)
}

// GetName returns the name of the action
func (o *StopOsdOps) GetName() string {
	return "stop-osd-ops"
}

// StartOsdOps is an operation to start osd service for a node.
type StartOsdOps struct {
	ClusterOps
}

// Run starts the osd service for a node.
func (o *StartOsdOps) Run(name string) error {
	err := SetOsdState(true)
	if err != nil {
		logger.Errorf("unable to start osd service in node '%s': %v", name, err)
		return err
	}
	logger.Infof("started osd service in node '%s'", name)
	return nil
}

// DryRun prints out the action plan.
func (o *StartOsdOps) DryRun(name string) string {
	return fmt.Sprintf("Start osd service in node '%s'.", name)
}

// GetName returns the name of the action
func (o *StartOsdOps) GetName() string {
	return "start-osd-ops"
}

// UnsetNooutOps is an operation to unset noout for the ceph cluster.
type UnsetNooutOps struct {
	ClusterOps
}

// Run `ceph osd unset noout` for the ceph cluster.
func (o *UnsetNooutOps) Run(name string) error {
	err := setOsdNooutFlag(false)
	if err != nil {
		return err
	}
	logger.Info("unset osd noout.")
	return nil
}

// DryRun prints out the action plan.
func (o *UnsetNooutOps) DryRun(name string) string {
	return "Run `ceph osd unset noout`."
}

// GetName returns the name of the action
func (o *UnsetNooutOps) GetName() string {
	return "unset-noout-ops"
}
