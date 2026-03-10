package main

import (
	"context"

	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/clilogger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterRemove struct {
	common  *CmdControl
	cluster *cmdCluster

	flagForce bool
}

func (c *cmdClusterRemove) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <NAME>",
		Short: "Removes a server from the cluster",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Forcibly remove the cluster member")

	return cmd
}

func (c *cmdClusterRemove) Run(cmd *cobra.Command, args []string) error {
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

	err = cli.DeleteClusterMember(context.Background(), args[0], c.flagForce)
	if err == nil {
		return nil
	}

	if c.flagForce && isDatabaseUpgradeWaitingError(err) {
		// During upgrade-waiting state the normal delete endpoint is blocked before
		// it reaches member-removal logic. Use the dedicated recovery endpoint.
		clilogger.Warnf("Standard force removal blocked by upgrade-waiting database, falling back to recovery removal path: %v", err)
		return client.ForceDeleteClusterMember(context.Background(), cli, args[0])
	}

	return err
}

// isDatabaseUpgradeWaitingError is a thin wrapper around common helper to keep
// command-level logic readable and directly unit-testable.
func isDatabaseUpgradeWaitingError(err error) bool {
	return common.IsDatabaseUpgradeWaitingError(err)
}
