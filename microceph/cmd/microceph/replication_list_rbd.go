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
	"golang.org/x/crypto/ssh/terminal"
)

type cmdReplicationListRbd struct {
	common   *CmdControl
	poolName string
	json     bool
}

func (c *cmdReplicationListRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd",
		Short: "List all rbd resources configured for replication.",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdReplicationListRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.ListReplicationRequest)
	if err != nil {
		return err
	}

	resp, err := client.SendReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	if c.json {
		fmt.Println(resp)
		return nil
	}

	return printReplicationList(resp)
}

func (c *cmdReplicationListRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	// list fetches ALL POOLS if pool name is empty.
	retReq := types.RbdReplicationRequest{
		SourcePool:   c.poolName,
		RequestType:  requestType,
		ResourceType: types.RbdResourcePool,
	}

	return retReq, nil
}

func printReplicationList(response string) error {
	var resp types.RbdPoolList
	err := json.Unmarshal([]byte(response), &resp)
	if err != nil {
		return nil
	}

	// start table object
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Pool Name", "Image Name", "Is Primary", "Last Local Update"}, rowConfigAutoMerge)
	for _, pool := range resp {
		for _, image := range pool.Images {
			t.AppendRow(table.Row{pool.Name, image.Name, image.IsPrimary, image.LastLocalUpdate}, rowConfigAutoMerge)
		}
	}
	if terminal.IsTerminal(0) && terminal.IsTerminal(1) {
		// Set style if interactive shell.
		t.SetStyle(table.StyleColoredBright)
	}
	t.Render()
	return nil
}
