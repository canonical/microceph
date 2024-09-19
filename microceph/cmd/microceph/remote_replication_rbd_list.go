package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

type cmdRemoteReplicationListRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
	json      bool
}

func (c *cmdRemoteReplicationListRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured remotes replication pairs.",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdRemoteReplicationListRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.ListReplicationRequest)
	if err != nil {
		return err
	}

	resp, err := client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	// TODO: remove this always true check.
	if c.json {
		fmt.Println(resp)
		return nil
	}

	return printRemoteReplicationList(resp)
}

func (c *cmdRemoteReplicationListRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
		SourcePool:  c.poolName,
		SourceImage: c.imageName,
		RequestType: requestType,
	}

	if len(c.poolName) != 0 && len(c.imageName) != 0 {
		retReq.ResourceType = types.RbdResourceImage
	} else {
		retReq.ResourceType = types.RbdResourcePool
	}

	return retReq, nil
}

func printRemoteReplicationList(response string) error {
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
