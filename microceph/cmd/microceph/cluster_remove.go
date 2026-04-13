package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
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

	members, err := m.GetClusterMembers(context.Background())
	if err != nil {
		return err
	}

	for _, member := range members {
		if member.Name == args[0] {
			return m.RemoveClusterMember(context.Background(), member.Name, member.Address.String(), c.flagForce)
		}
	}

	return fmt.Errorf("cluster member %q not found", args[0])
}
