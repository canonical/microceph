package main

import (
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

	clusterClient, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = ceph.EnterMaintenance(clusterClient, client.MClient, args[0], c.flagForce, c.flagDryRun, c.flagSetNoout, c.flagStopOsds)
	if err != nil {
		return err
	}

	return nil
}
