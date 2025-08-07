package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationListCephfs struct {
	common *CmdControl
	json   bool
}

func (c *cmdReplicationListCephfs) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd",
		Short: "List all rbd resources configured for replication.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdReplicationListCephfs) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
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

	payload := types.CephfsReplicationRequest{
		RequestType: types.ListReplicationRequest,
	}
	resp, err := client.SendReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	if c.json {
		fmt.Println(resp)
		return nil
	}

	return printCephfsReplicationList(resp)
}

func printCephfsReplicationList(response string) error {
	return fmt.Errorf("list table is not implemented for CephFs Mirror resources")
}
