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
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type cmdReplicationListCephfs struct {
	common *CmdControl
	json   bool
}

func (c *cmdReplicationListCephfs) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cephfs",
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
	var resp types.CephFsReplicationResponseList
	err := json.Unmarshal([]byte(response), &resp)
	if err != nil {
		return nil
	}

	// start table object
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Volume", "Resource", "Type"}, rowConfigAutoMerge)
	for volume, resources := range resp {
		for _, resource := range resources {
			t.AppendRow(table.Row{volume, resource.ResourcePath, resource.ResourceType}, rowConfigAutoMerge)
		}
	}
	t.Render()
	return nil
}
