package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationStatusCephfs struct {
	common *CmdControl
	subvolume string
	dirPath   string
	json   bool
}

func (c *cmdReplicationStatusCephfs) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cephfs <volume>",
		Short: "Show CephFS resource (directory or subvolume) replication status",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.subvolume, "subvolume", "", "subvolume name")
	cmd.Flags().StringVar(&c.dirPath, "dir-path", "", "directory path in the CephFS volume")
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

	return printCephfsReplicationStatusTable(payload.ResourceType, resp)
}

func (c *cmdReplicationStatusCephfs) prepareCephfsPayload(requestType types.ReplicationRequestType, args []string) (types.CephfsReplicationRequest, error) {
	retReq := types.CephfsReplicationRequest{
		Volume:   args[0],
		Subvolume: c.subvolume,
		DirPath:  c.dirPath,
		RequestType:  requestType,
		ResourceType: types.GetCephfsResourceType(c.subvolume, c.dirPath),
	}

	return retReq, nil
}

func printCephfsReplicationStatusTable(ResourceType types.CephfsResourceType, _ string) error {
	return fmt.Errorf("status table is not implemented for %s", ResourceType)
}
