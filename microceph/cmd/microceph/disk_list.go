package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/units"
	microCli "github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
)

type cmdDiskList struct {
	common *CmdControl
	disk   *cmdDisk
	json   bool
}

func (c *cmdDiskList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List servers in the cluster",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().BoolVar(&c.json, "json", false, "Provide output as Json encoded string.")
	return cmd
}

type Disk struct {
	Model string
	Size  string
	Type  string
	Path  string
}

// Structure for marshalling to json.
type DiskListOutput struct {
	ConfiguredDisks    types.Disks
	UnpartitionedDisks []Disk
}

func (c *cmdDiskList) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// List configured disks.
	disks, err := client.GetDisks(context.Background(), cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch configured disks: %w", err)
	}

	if c.json {
		return outputJson(cli)
	}

	data := make([][]string, len(disks))
	for i, disk := range disks {
		data[i] = []string{fmt.Sprintf("%d", disk.OSD), disk.Location, disk.Path}
	}

	header := []string{"OSD", "LOCATION", "PATH"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))

	fmt.Println("Disks configured in MicroCeph:")
	err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, disks)
	if err != nil {
		return err
	}

	// List local disks.
	err = listLocalDisks(cli)
	if err != nil {
		return err
	}

	return nil
}

func listLocalDisks(cli *microCli.Client) error {
	data := [][]string{}

	disks, err := getUnpartitionedDisks(cli)
	if err != nil {
		return err
	}

	for _, disk := range disks {
		data = append(data, []string{disk.Model, disk.Size, disk.Type, disk.Path})
	}

	fmt.Println("")
	fmt.Println("Available unpartitioned disks on this system:")

	header := []string{"MODEL", "CAPACITY", "TYPE", "PATH"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))

	err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, data)
	if err != nil {
		return err
	}

	return nil
}

// outputJson prints the json output to stdout.
func outputJson(cli *microCli.Client) error {
	output := DiskListOutput{}
	var err error

	// List configured disks.
	output.ConfiguredDisks, err = client.GetDisks(context.Background(), cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch configured disks: %w", err)
	}

	output.UnpartitionedDisks, err = getUnpartitionedDisks(cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch unpartitoned disks: %w", err)
	}

	opStr, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("internal error: unable to encode json output: %w", err)
	}

	fmt.Printf("%s\n", opStr)
	return nil
}

// getUnpartitionedDisks fetches the list of available resources
func getUnpartitionedDisks(cli *microCli.Client) ([]Disk, error) {
	// List configured disks.
	disks, err := client.GetDisks(context.Background(), cli)
	if err != nil {
		return nil, fmt.Errorf("internal error: unable to fetch configured disks: %w", err)
	}

	// List physical disks.
	resources, err := client.GetResources(context.Background(), cli)
	if err != nil {
		return nil, fmt.Errorf("internal error: unable to fetch available disks: %w", err)
	}

	// Get local hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("internal error: unable to fetch Hostname: %w", err)
	}

	// Prepare the table.
	data := []Disk{}
	for _, disk := range resources.Disks {
		if len(disk.Partitions) > 0 {
			continue
		}

		if len(disk.DeviceID) == 0 {
			continue
		}

		// Minimum size set to 2GB i.e. 2*1024*1024*1024
		if disk.Size < common.MinOSDSize {
			logger.Debugf("Ignoring device %s, size less than 2GB", disk.DeviceID)
			continue
		}

		devicePath := fmt.Sprintf("%s%s", common.DevicePathPrefix, disk.DeviceID)

		found := false
		// check if disk already employed as an OSD.
		for _, entry := range disks {
			if entry.Location != hostname {
				continue
			}

			if entry.Path == devicePath {
				found = true
				break
			}
		}

		if found {
			continue
		}

		data = append(data, Disk{
			Model: disk.Model,
			Size:  units.GetByteSizeStringIEC(int64(disk.Size), 2),
			Type:  disk.Type,
			Path:  devicePath,
		})
	}

	return data, nil
}
