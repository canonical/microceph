package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type cmdRemoteList struct {
	common *CmdControl
	json   bool
}

func (c *cmdRemoteList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured remotes for the site",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdRemoteList) Run(cmd *cobra.Command, args []string) error {
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

	// Read remote cluster token
	data, err := client.FetchAllRemotes(context.Background(), cli)
	if err != nil {
		return fmt.Errorf("failed to fetch remotes: %w", err)
	}

	if c.json {
		return printRemotesJson(data)
	}

	return printRemoteTable(data)
}

func printRemotesJson(remotes []types.RemoteRecord) error {
	opStr, err := json.Marshal(remotes)
	if err != nil {
		return fmt.Errorf("internal error: unable to encode json output: %w", err)
	}

	fmt.Printf("%s\n", opStr)
	return nil
}

func printRemoteTable(remotes []types.RemoteRecord) error {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"ID", "Remote Name", "Local Name"})
	for _, remote := range remotes {
		t.AppendRow(table.Row{remote.ID, remote.Name, remote.LocalName})
	}
	t.SetStyle(table.StyleColoredBright)
	t.Render()
	return nil
}
