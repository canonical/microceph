package ceph

import (
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
)

type Maintenance struct {
	Node       string
	ClusterOps ClusterOps
}

// Exit brings the node out of maintenance mode.
func (m *Maintenance) Exit(req types.MaintenanceRequest) ([]Result, error) {
	// General sanity checks
	if req.CheckOnly && req.IgnoreCheck {
		err := fmt.Errorf("'check-only' and 'ignore-check' are mutually exclusive.")
		return []Result{}, err
	}

	// Preflight checks for exiting maintenance mode (currently empty)
	preflightChecks := []Operation{}

	// Main operations
	operations := []Operation{
		&UnsetNooutOps{ClusterOps: m.ClusterOps},
		&AssertNooutFlagUnsetOps{ClusterOps: m.ClusterOps},
		&StartOsdOps{ClusterOps: m.ClusterOps},
	}

	// Execute the maintenance operations
	results := []Result{}
	if req.CheckOnly {
		// Only run preflight checks
		results = append(results, RunOperations(m.Node, preflightChecks, req.DryRun, false)...)
	} else if req.IgnoreCheck {
		// Only run main operations (ignore preflight checks)
		results = append(results, RunOperations(m.Node, operations, req.DryRun, false)...)
	} else {
		// Run both preflight checks and main operations
		results = append(results, RunOperations(m.Node, preflightChecks, req.DryRun, false)...)
		// Return the result now if there's error in preflight checks
		for _, result := range results {
			if result.Error != "" {
				return results, nil // the error is not for operation error
			}
		}
		// Otherwise, continue with the main operations
		results = append(results, RunOperations(m.Node, operations, req.DryRun, false)...)
	}

	return results, nil
}

// Enter brings the node into maintenance mode.
func (m *Maintenance) Enter(req types.MaintenanceRequest) ([]Result, error) {
	// General sanity checks
	if req.CheckOnly && req.IgnoreCheck {
		err := fmt.Errorf("'check-only' and 'ignore-check' are mutually exclusive.")
		return []Result{}, err
	}

	// Preflight checks for entering maintenance mode
	preflightChecks := []Operation{
		&CheckOsdOkToStopOps{ClusterOps: m.ClusterOps},
		&CheckNonOsdSvcEnoughOps{ClusterOps: m.ClusterOps, MinMon: 3, MinMds: 1, MinMgr: 1},
	}

	// Main operations
	operations := []Operation{}
	// Optionally add "set noout op" to main operations
	if req.SetNoout {
		operations = append(operations, []Operation{
			&SetNooutOps{ClusterOps: m.ClusterOps},
			&AssertNooutFlagSetOps{ClusterOps: m.ClusterOps},
		}...)
	}
	// Optionally add "stop osd service op" to main operations
	if req.StopOsds {
		operations = append(operations, []Operation{
			&StopOsdOps{ClusterOps: m.ClusterOps},
		}...)
	}

	// Execute the maintenance operations
	results := []Result{}
	if req.CheckOnly {
		// Only run preflight checks
		results = append(results, RunOperations(m.Node, preflightChecks, req.DryRun, false)...)
	} else if req.IgnoreCheck {
		// Only run main operations (ignore preflight checks)
		results = append(results, RunOperations(m.Node, operations, req.DryRun, false)...)
	} else {
		// Run both preflight checks and main operations
		results = append(results, RunOperations(m.Node, preflightChecks, req.DryRun, false)...)
		// Return the result now if there's error in preflight checks
		for _, result := range results {
			if result.Error != "" && !req.Force {
				return results, nil // the error is not for operation error
			}
		}
		// Otherwise, continue with the main operations
		results = append(results, RunOperations(m.Node, operations, req.DryRun, false)...)
	}

	return results, nil
}
