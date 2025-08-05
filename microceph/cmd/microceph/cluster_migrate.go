package main

import (
	"context"

	"github.com/canonical/microceph/microceph/clilogger"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterMigrate struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate <SRC> <DST",
		Short: "Migrate automatic services from one node to another",
		RunE:  c.Run,
	}
	return cmd
}

func (c *cmdClusterMigrate) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
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

	autoServices := []string{"mds", "mgr", "mon"}
	// Enable auto services on dst node
	req := &types.EnableService{
		Wait:    true,
		Payload: "",
	}
	for _, service := range autoServices {
		req.Name = service
		clilogger.Infof("Enabling %s on %s", service, args[1])
		err = client.SendServicePlacementReq(context.Background(), cli, req, args[1])
		if err != nil {
			clilogger.Errorf("Failed to enable %s on %s, bailing: %v", service, args[1], err)
			return err
		}
	}

	// Disable auto services on src node
	for _, service := range autoServices {
		req.Name = service
		clilogger.Infof("Disabling %s on %s", service, args[0])
		err = client.DeleteService(context.Background(), cli, args[0], service)
		if err != nil {
			clilogger.Errorf("Failed to disable %s on %s, bailing: %v", service, args[0], err)
			return err
		}
	}

	return nil

}
