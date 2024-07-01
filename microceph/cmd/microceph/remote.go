package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemote struct {
	common *CmdControl
}

func (c *cmdRemote) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage MicroCeph remotes",
	}

	// Config Subcommand
	remoteImportCmd := cmdRemoteImport{common: c.common, remote: c}
	cmd.AddCommand(remoteImportCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

type cmdRemoteImport struct {
	common *CmdControl
	remote *cmdRemote
}

type dict map[string]interface{}

func (c *cmdRemoteImport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote import <Name> <ConfigFilePath>",
		Short: "Import external MicroCeph cluster as a remote",
	}

	return cmd
}

func (c *cmdRemoteImport) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return cmd.Help()
	}

	// read the cluster config file
	data := dict{}
	content, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}

	err = json.Unmarshal(content, &data)
	if err != nil {
		return err
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// send remote import request
	return client.SendRemoteImportRequest(context.Background(), cli, data)
}
