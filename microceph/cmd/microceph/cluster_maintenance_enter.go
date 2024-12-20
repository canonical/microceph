package main

import (
	"fmt"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterMaintenanceEnter struct {
	common *CmdControl

	flagForce    bool
	flagDryRun   bool
	flagSetNoout bool
	flagStopOsds bool
}

func (c *cmdClusterMaintenanceEnter) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enter <NAME>",
		Short: "Enter maintenance mode.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagForce, "force", false, "Force to enter maintenance mode.")
	cmd.Flags().BoolVar(&c.flagDryRun, "dry-run", false, "Dry run the command.")
	cmd.Flags().BoolVar(&c.flagSetNoout, "set-noout", true, "Stop CRUSH from rebalancing the cluster.")
	cmd.Flags().BoolVar(&c.flagStopOsds, "stop-osds", false, "Stop the OSDS when entering maintenance mode.")
	return cmd
}

func (c *cmdClusterMaintenanceEnter) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	name := args[0]
	operations := []ceph.Operation{
		&ceph.CheckNodeInClusterOps{client.MClient, cli},
	}

	// pre-flight checks
	if !c.flagForce {
		operations = append(operations, []ceph.Operation{
			&ceph.CheckOsdOkToStopOps{client.MClient, cli},
			&ceph.CheckNonOsdSvcEnoughOps{client.MClient, cli, 3, 1, 1},
		}...)
	}

	// optionally set noout
	if c.flagSetNoout {
		operations = append(operations, []ceph.Operation{
			&ceph.SetNooutOps{},
			&ceph.AssertNooutFlagSetOps{},
		}...)
	}

	// optionally stop osd service
	if c.flagStopOsds {
		operations = append(operations, []ceph.Operation{
			&ceph.StopOsdOps{client.MClient, cli},
		}...)
	}

	err = ceph.RunOperations(name, operations, c.flagDryRun)
	if err != nil {
		return fmt.Errorf("Failed to enter maintenance mode: %v", err)
	}

	return nil
}
