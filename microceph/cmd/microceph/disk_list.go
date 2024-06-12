package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/constants"
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
	ConfiguredDisks types.Disks
	AvailableDisks  []Disk
}

func (c *cmdDiskList) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// List configured disks.
	configuredDisks, err := client.GetDisks(context.Background(), cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch configured disks: %w", err)
	}

	// List unpartitioned disks.
	availableDisks, err := getUnpartitionedDisks(cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch unpartitoned disks: %w", err)
	}

	if c.json {
		return outputJson(configuredDisks, availableDisks)
	}

	return outputFormattedTable(configuredDisks, availableDisks)
}

func outputFormattedTable(configuredDisks types.Disks, availableDisks []Disk) error {
	var err error

	if len(configuredDisks) > 0 {
		// Print configured disks.
		cData := make([][]string, len(configuredDisks))
		for i, cDisk := range configuredDisks {
			cData[i] = []string{fmt.Sprintf("%d", cDisk.OSD), cDisk.Location, cDisk.Path}
		}

		header := []string{"OSD", "LOCATION", "PATH"}
		sort.Sort(lxdCmd.SortColumnsNaturally(cData))

		fmt.Println("Disks configured in MicroCeph:")
		err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, cData, configuredDisks)
		if err != nil {
			return err
		}
	}

	if len(availableDisks) > 0 {
		// Print available disks
		aData := make([][]string, len(availableDisks))
		for i, aDisk := range availableDisks {
			aData[i] = []string{aDisk.Model, aDisk.Size, aDisk.Type, aDisk.Path}
		}

		header := []string{"MODEL", "CAPACITY", "TYPE", "PATH"}
		sort.Sort(lxdCmd.SortColumnsNaturally(aData))

		fmt.Println("\nAvailable unpartitioned disks on this system:")
		err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, aData, aData)
		if err != nil {
			return err
		}
	}

	return nil
}

// outputJson prints the json output to stdout.
func outputJson(configuredDisks types.Disks, availableDisks []Disk) error {
	var err error
	output := DiskListOutput{
		ConfiguredDisks: configuredDisks,
		AvailableDisks:  availableDisks,
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

	data, err := filterLocalDisks(resources, disks)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// filterLocalDisks filters out the disks that are in use or otherwise not suitable for OSDs.
func filterLocalDisks(resources *api.ResourcesStorage, disks types.Disks) ([]Disk, error) {
	var err error
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
		if disk.Size < constants.MinOSDSize {
			logger.Debugf("Ignoring device %s, size less than 2GB", disk.DeviceID)
			continue
		}

		devicePath := fmt.Sprintf("%s%s", constants.DevicePathPrefix, disk.DeviceID)

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

		// check if disk is mounted or already employed as a journal or db
		mounted, err := common.IsMounted(devicePath)
		if err != nil {
			return nil, fmt.Errorf("internal error: unable to check if disk is mounted: %w", err)
		}
		if mounted {
			continue
		}
		isCephDev, err := common.IsCephDevice(devicePath)
		if err != nil {
			return nil, fmt.Errorf("internal error checking if disk is ceph device: %w", err)
		}
		if isCephDev {
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
