package main

import (
	"encoding/base64"
	"fmt"

	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterExport struct {
	common  *CmdControl
	cluster *cmdCluster
	json    bool
}

func (c *cmdClusterExport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Generates a base64 dump of cluster state",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdClusterExport) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	state, err := client.GetClusterState(cmd.Context(), cli)
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
