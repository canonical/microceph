package main

import (
	"encoding/base64"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterExport struct {
	common  *CmdControl
	cluster *cmdCluster
	json    bool
}

func (c *cmdClusterExport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <remote-name>",
		Short: "Generates cluster token for Remote cluster with given name",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdClusterExport) Run(cmd *cobra.Command, args []string) error {
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

	state, err := client.GetClusterToken(cmd.Context(), cli, types.ClusterExportRequest{
		RemoteName: args[0],
	})
	if err != nil {
		return err
	}

	// produce output in CLI.
	if c.json {
		jsonOut, err := base64.StdEncoding.DecodeString(state)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", jsonOut)
	} else {
		fmt.Println(state)
	}

	return nil
}
