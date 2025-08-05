package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/canonical/lxd/shared/api"
	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/microceph/microceph/clilogger"
	microCli "github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
)

type cmdDiskList struct {
	common   *CmdControl
	disk     *cmdDisk
	json     bool
	hostOnly bool
}

func (c *cmdDiskList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List disks configured in MicroCeph and available unpartitioned disks on this system.",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().BoolVar(&c.json, "json", false, "Provide output as Json encoded string.")
	cmd.PersistentFlags().BoolVar(&c.hostOnly, "host-only", false, "Output only the disks configured on current host.")
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
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
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
	clilogger.Debugf("Found %d configured disks", len(configuredDisks))

	// List unpartitioned disks.
	availableDisks, err := getUnpartitionedDisks(cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch unpartitoned disks: %w", err)
	}
	clilogger.Debugf("Found %d unpartitioned disks", len(availableDisks))

	if c.hostOnly {
		fcg := types.Disks{}

		// Get system hostname.
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("failed to retrieve system hostname: %w", err)
		}

		for _, disk := range configuredDisks {
			if disk.Location == hostname {
				fcg = append(fcg, disk)
			}
		}

		// Overwrite configured disks with the filtered disks.
		configuredDisks = fcg
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
	return doFilterLocalDisks(resources, disks, common.IsMounted, common.IsCephDevice)
}

// doFilterLocalDisks filters local disks but allows dep. injection for testing.
func doFilterLocalDisks(resources *api.ResourcesStorage, disks types.Disks,
	isMountedFunc func(string) (bool, error),
	isCephDeviceFunc func(string) (bool, error)) ([]Disk, error) {
	var err error
	// Get local hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("internal error: unable to fetch Hostname: %w", err)
	}

	// Prepare the table.
	data := []Disk{}
	for _, disk := range resources.Disks {
		clilogger.Debugf("Checking disk %s, size %d, type %s", disk.ID, disk.Size, disk.Type)
		if len(disk.Partitions) > 0 {
			clilogger.Infof("Ignoring device %s, it has partitions", disk.ID)
			continue
		}

		// Minimum size set to 2GB i.e. 2*1024*1024*1024
		if disk.Size < constants.MinOSDSize {
			clilogger.Infof("Ignoring device %s, size less than 2GB", disk.ID)
			continue
		}

		// Try to construct device path using multiple fallback methods
		var devicePath string
		if len(disk.DeviceID) > 0 {
			// First preference: use DeviceID with prefix (e.g., /dev/disk/by-id/...)
			devicePath = fmt.Sprintf("%s%s", constants.DevicePathPrefix, disk.DeviceID)
		} else if len(disk.DevicePath) > 0 {
			// Second preference: use DevicePath (e.g., /dev/disk/by-path/...)
			devicePath = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
		} else {
			// Final fallback: use device ID directly (e.g., /dev/vdc)
			devicePath = fmt.Sprintf("/dev/%s", disk.ID)
		}

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
		mounted, err := isMountedFunc(devicePath)
		if err != nil {
			return nil, fmt.Errorf("internal error: unable to check if disk is mounted: %w", err)
		}
		if mounted {
			continue
		}
		isCephDev, err := isCephDeviceFunc(devicePath)
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
