package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

type cmdRemoteReplicationStatusRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
	json      bool
}

func (c *cmdRemoteReplicationStatusRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show RBD Pool or Image replication status",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdRemoteReplicationStatusRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.StatusReplicationRequest)
	if err != nil {
		return err
	}

	resp, err := client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	if c.json {
		fmt.Println(resp)
		return nil
	}

	return printRemoteReplicationStatusTable(payload.ResourceType, resp)
}

func (c *cmdRemoteReplicationStatusRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
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

func printRemoteReplicationStatusTable(ResourceType types.RbdResourceType, response string) error {
	var err error

	// start table object
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter}

	if ResourceType == types.RbdResourcePool {
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

		// Images Section.
		// t_images := table.NewWriter()
		// t_images.SetOutputMirror(os.Stdout)
		// t_images.AppendHeader(table.Row{"Image Name", "Is Primary", "Last Local Update"})
		// for _, image := range resp.Images {
		// 	t_images.AppendRow(table.Row{image.Name, image.IsPrimary, image.LastLocalUpdate})
		// }
		// t_images.SetStyle(table.StyleColoredBright)
		// t_images.Render()
		// fmt.Println()

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

	} else if ResourceType == types.RbdResourceImage {
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
