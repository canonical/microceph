package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/canonical/microcluster/microcluster"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

type cmdDiskList struct {
	common *CmdControl
	disk   *cmdDisk
}

func (c *cmdDiskList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List servers in the cluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdDiskList) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	disks, err := client.GetDisks(context.Background(), cli)
	if err != nil {
		return err
	}

	data := make([][]string, len(disks))
	for i, disk := range disks {
		data[i] = []string{fmt.Sprintf("%d", disk.OSD), disk.Location, disk.Path}
	}

	header := []string{"OSD", "LOCATION", "PATH"}
	sort.Sort(utils.ByName(data))

	return utils.RenderTable(utils.TableFormatTable, header, data, disks)
}
