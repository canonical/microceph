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

type cmdReplicationStatusCephfs struct {
	common *CmdControl
	json   bool
}

func (c *cmdReplicationStatusCephfs) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cephfs <volume>",
		Short: "Show CephFS resource (directory or subvolume) replication status",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdReplicationStatusCephfs) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareCephfsPayload(types.StatusReplicationRequest, args)
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

	return c.printCephfsReplicationStatusTable(resp)
}

func (c *cmdReplicationStatusCephfs) prepareCephfsPayload(requestType types.ReplicationRequestType, args []string) (types.CephfsReplicationRequest, error) {
	retReq := types.CephfsReplicationRequest{
		Volume:      args[0],
		RequestType: requestType,
	}

	return retReq, nil
}

func (c *cmdReplicationStatusCephfs) printCephfsReplicationStatusTable(apiResponse string) error {
	var resp types.CephFsReplicationResponseStatus
	err := json.Unmarshal([]byte(apiResponse), &resp)
	if err != nil {
		return err
	}

	rowConfig := table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter}

	// Summary Section.
	t_summary := table.NewWriter()
	t_summary.SetOutputMirror(os.Stdout)
	t_summary.AppendHeader(table.Row{"Summary", "Summary"}, rowConfig)
	t_summary.AppendRow(table.Row{"Volume", resp.Volume}, rowConfig)
	t_summary.AppendRow(table.Row{"Resource Count", resp.MirrorResourceCount}, rowConfig)
	t_summary.AppendRow(table.Row{"Peer Count", len(resp.Peers)}, rowConfig)
	t_summary.Render()

	fmt.Println() // Add a newline for better readability

	// Peers Section
	t_remotes := table.NewWriter()
	t_remotes.SetOutputMirror(os.Stdout)
	t_remotes.AppendHeader(table.Row{"Remote Name", "Resource Path", "State", "Snaps Synced", "Snaps Deleted", "Snaps Renamed"}, rowConfig)
	for _, peer := range resp.Peers {
		for resourcePath, status := range peer.MirrorStatus {
			t_remotes.AppendRow(table.Row{
				peer.Name,
				resourcePath,
				status.State,
				status.Synced,
				status.Deleted,
				status.Renamed,
			}, rowConfig)
		}
	}
	t_remotes.Render()
	return nil
}
