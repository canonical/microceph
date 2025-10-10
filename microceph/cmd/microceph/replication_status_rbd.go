package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

type cmdReplicationStatusRbd struct {
	common *CmdControl
	json   bool
}

func (c *cmdReplicationStatusRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd <resource>",
		Short: "Show RBD resource replication status",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdReplicationStatusRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.StatusReplicationRequest, args)
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

	return printReplicationStatusTable(payload.ResourceType, resp)
}

func (c *cmdReplicationStatusRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := types.GetPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		SourcePool:   pool,
		SourceImage:  image,
		RequestType:  requestType,
		ResourceType: types.GetRbdResourceType(pool, image),
	}

	return retReq, nil
}

func printReplicationStatusTable(ResourceType types.RbdResourceType, response string) error {
	var err error

	// start table object
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter}

	switch ResourceType {
	case types.RbdResourcePool:
		var resp types.RbdPoolStatus
		err = json.Unmarshal([]byte(response), &resp)
		if err != nil {
			return err
		}

		// Summary Section.
		t_summary := table.NewWriter()
		t_summary.SetOutputMirror(os.Stdout)
		t_summary.AppendHeader(table.Row{"Summary", "Summary", "Health", "Health"}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Name", resp.Name, "Replication", resp.HealthReplication}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Mode", resp.Type, "Daemon", resp.HealthDaemon}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Image Count", resp.ImageCount, "Image", resp.HealthImages}, rowConfigAutoMerge)
		if terminal.IsTerminal(0) && terminal.IsTerminal(1) {
			// Set style if interactive shell.
			t_summary.SetStyle(table.StyleColoredBright)
		}
		t_summary.Render()
		fmt.Println()

		// Remotes Section
		t_remotes := table.NewWriter()
		t_remotes.SetOutputMirror(os.Stdout)
		t_remotes.AppendHeader(table.Row{"Remote Name", "Direction", "UUID"})
		for _, remote := range resp.Remotes {
			t_remotes.AppendRow(table.Row{remote.Name, remote.Direction, remote.UUID})
		}
		if terminal.IsTerminal(0) && terminal.IsTerminal(1) {
			// Set style if interactive shell.
			t_remotes.SetStyle(table.StyleColoredBright)
		}
		t_remotes.Render()
		fmt.Println()

	case types.RbdResourceImage:
		var resp types.RbdImageStatus
		err = json.Unmarshal([]byte(response), &resp)
		if err != nil {
			return err
		}

		// Summary Section.
		t_summary := table.NewWriter()
		t_summary.SetOutputMirror(os.Stdout)
		t_summary.AppendHeader(table.Row{"Summary", "Summary"}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Name", resp.Name}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"ID", resp.ID}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Mode", resp.Type}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Is Primary", resp.IsPrimary}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Status", resp.Status}, rowConfigAutoMerge)
		t_summary.AppendRow(table.Row{"Last Local Update", resp.LastLocalUpdate}, rowConfigAutoMerge)
		t_summary.SetStyle(table.StyleColoredBright)
		t_summary.Render()
		fmt.Println()

		// Images Section.
		t_images := table.NewWriter()
		t_images.SetOutputMirror(os.Stdout)
		t_images.AppendHeader(table.Row{"Remote Name", "Status", "Last Remote Update"}, rowConfigAutoMerge)
		for _, remote := range resp.Remotes {
			var status string
			statusList := strings.Split(remote.Status, ",")
			if len(statusList) < 1 {
				status = ""
			} else {
				status = statusList[0]
			}
			t_images.AppendRow(table.Row{remote.Name, status, remote.LastRemoteUpdate})
		}
		if terminal.IsTerminal(0) && terminal.IsTerminal(1) {
			// Set style if interactive shell.
			t_images.SetStyle(table.StyleColoredBright)
		}
		t_images.Render()
		fmt.Println()
	}
	return nil
}
