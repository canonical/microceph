package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationEnableCephFS struct {
	common         *CmdControl
	remoteName     string
	volume         string
	dirpath        string
	subvolume      string
	subvolumegroup string
}

func (c *cmdReplicationEnableCephFS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rbd <resource>",
		Short:   "Enable replication for RBD resource (Pool or Image)",
		RunE:    c.Run,
		PreRunE: c.PreRun, // Validate flags
	}

	cmd.Flags().StringVar(&c.remoteName, "remote", "", "remote MicroCeph cluster name")
	cmd.Flags().StringVar(&c.volume, "volume", "", "CephFS volume (aka file-system)")
	cmd.Flags().StringVar(&c.subvolumegroup, "subvolumegroup", "", "CephFS Subvolume Group")
	cmd.Flags().StringVar(&c.subvolume, "subvolume", "", "CephFS Subvolume")
	cmd.Flags().StringVar(&c.dirpath, "dir-path", "", "Directory path relative to file system")

	cmd.MarkFlagRequired("remote")
	cmd.MarkFlagRequired("volume")
	cmd.MarkFlagsOneRequired("dir-path", "subvolume")

	cmd.MarkFlagsMutuallyExclusive("dir-path", "subvolumegroup")
	cmd.MarkFlagsMutuallyExclusive("dir-path", "subvolume")
	return cmd
}

func (c *cmdReplicationEnableCephFS) PreRun(cmd *cobra.Command, args []string) error {
	subvolumegroup, _ := cmd.Flags().GetString("subvolumegroup")
	if len(subvolumegroup) != 0 {
		cmd.MarkFlagRequired("subvolume")
	}
	return nil
}

func (c *cmdReplicationEnableCephFS) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareCephfsPayload()
	if err != nil {
		return err
	}

	_, err = client.SendReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdReplicationEnableCephFS) prepareCephfsPayload() (types.CephfsReplicationRequest, error) {
	retReq := types.CephfsReplicationRequest{
		Volume:         c.volume,
		Subvolume:      c.subvolume,
		SubvolumeGroup: c.subvolumegroup,
		DirPath:        c.dirpath,
		RemoteName:     c.remoteName,
		RequestType:    types.EnableReplicationRequest,
		ResourceType:   getCephFSResourceType(c.subvolume, c.dirpath),
	}

	return retReq, nil
}

func getCephFSResourceType(subvolume string, dirpath string) types.CephfsResourceType {
	if len(subvolume) != 0 {
		return types.CephfsResourceSubvolume
	} else if len(dirpath) != 0 {
		return types.CephfsResourceDirectory
	}

	return types.CephfsResourceInvalid
}
