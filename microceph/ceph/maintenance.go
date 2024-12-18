package ceph

import (
	"fmt"
)

type Maintenance struct {
	Node       string
	ClusterOps ClusterOps
}

// Exit brings the node out of maintenance mode.
func (m *Maintenance) Exit(dryRun, checkOnly, ignoreCheck bool) ([]Result, error) {
	// General sanity checks
	if checkOnly && ignoreCheck {
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
	if checkOnly {
		// Only run preflight checks
		results = append(results, RunOperations(m.Node, preflightChecks, dryRun, false)...)
	} else if ignoreCheck {
		// Only run main operations (ignore preflight checks)
		results = append(results, RunOperations(m.Node, operations, dryRun, false)...)
	} else {
		// Run both preflight checks and main operations
		results = append(results, RunOperations(m.Node, preflightChecks, dryRun, false)...)
		// Return the result now if there's error in preflight checks
		for _, result := range results {
			if result.Error != "" {
				return results, nil // the error is not for operation error
			}
		}
		// Otherwise, continue with the main operations
		results = append(results, RunOperations(m.Node, operations, dryRun, false)...)
	}

	return results, nil
}

// Enter brings the node into maintenance mode.
func (m *Maintenance) Enter(force, dryRun, setNoout, stopOsds, checkOnly, ignoreCheck bool) ([]Result, error) {
	// General sanity checks
	if checkOnly && ignoreCheck {
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
	if setNoout {
		operations = append(operations, []Operation{
			&SetNooutOps{ClusterOps: m.ClusterOps},
			&AssertNooutFlagSetOps{ClusterOps: m.ClusterOps},
		}...)
	}
	// Optionally add "stop osd service op" to main operations
	if stopOsds {
		operations = append(operations, []Operation{
			&StopOsdOps{ClusterOps: m.ClusterOps},
		}...)
	}

	// Execute the maintenance operations
	results := []Result{}
	if checkOnly {
		// Only run preflight checks
		results = append(results, RunOperations(m.Node, preflightChecks, dryRun, false)...)
	} else if ignoreCheck {
		// Only run main operations (ignore preflight checks)
		results = append(results, RunOperations(m.Node, operations, dryRun, false)...)
	} else {
		// Run both preflight checks and main operations
		results = append(results, RunOperations(m.Node, preflightChecks, dryRun, false)...)
		// Return the result now if there's error in preflight checks
		for _, result := range results {
			if result.Error != "" && !force {
				return results, nil // the error is not for operation error
			}
		}
		// Otherwise, continue with the main operations
		results = append(results, RunOperations(m.Node, operations, dryRun, false)...)
	}

	return results, nil
}
