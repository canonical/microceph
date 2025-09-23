package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationDisableCephFS struct {
	common *CmdControl

	volume         string
	subvolume      string
	subvolumegroup string
	dirpath        string
}

func (c *cmdReplicationDisableCephFS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd <resource>",
		Short: "Disable replication for RBD resource (Pool or Image)",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.volume, "volume", "", "CephFS volume (aka file-system)")
	cmd.Flags().StringVar(&c.subvolumegroup, "subvolumegroup", "", "CephFS Subvolume Group")
	cmd.Flags().StringVar(&c.subvolume, "subvolume", "", "CephFS Subvolume")
	cmd.Flags().StringVar(&c.dirpath, "dir-path", "", "Directory path relative to file system")

	cmd.MarkFlagRequired("volume")
	cmd.MarkFlagsOneRequired("dir-path", "subvolume")
	cmd.MarkFlagsMutuallyExclusive("dir-path", "subvolumegroup")
	cmd.MarkFlagsMutuallyExclusive("dir-path", "subvolume")

	return cmd
}

func (c *cmdReplicationDisableCephFS) PreRun(cmd *cobra.Command, args []string) error {
	subvolumegroup, _ := cmd.Flags().GetString("subvolumegroup")
	if len(subvolumegroup) != 0 {
		cmd.MarkFlagRequired("subvolume")
	}
	return nil
}

func (c *cmdReplicationDisableCephFS) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareCephFSPayload()
	if err != nil {
		return err
	}

	_, err = client.SendReplicationRequest(context.Background(), cli, payload)
	return err
}

func (c *cmdReplicationDisableCephFS) prepareCephFSPayload() (types.CephfsReplicationRequest, error) {
	retReq := types.CephfsReplicationRequest{
		Volume:         c.volume,
		Subvolume:      c.subvolume,
		SubvolumeGroup: c.subvolumegroup,
		DirPath:        c.dirpath,
		RequestType:    types.DisableReplicationRequest,
		ResourceType:   getCephFSResourceType(c.subvolume, c.dirpath),
	}

	return retReq, nil
}
