package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/canonical/lxd/shared/api"
	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/microceph/microceph/clilogger"
	"github.com/canonical/microcluster/v3/microcluster"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
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

	memberName, err := localMemberName(m)
	if err != nil {
		return err
	}

	// List unpartitioned disks.
	availableDisks, err := getUnpartitionedDisks(cli, memberName)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch unpartitoned disks: %w", err)
	}
	clilogger.Debugf("Found %d unpartitioned disks", len(availableDisks))

	if c.hostOnly {
		// Overwrite configured disks with the filtered disks.
		configuredDisks = disksOnMember(configuredDisks, memberName)
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

// localMemberName returns the cluster member name of this node. Configured
// disks record it as their location, and it differs from the hostname when a
// custom name was chosen at init or the host was renamed after joining.
func localMemberName(m *microcluster.MicroCluster) (string, error) {
	status, err := m.Status(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to retrieve the local cluster member name: %w", err)
	}

	return status.Name, nil
}

// disksOnMember returns the configured disks located on the given cluster member.
func disksOnMember(disks types.Disks, memberName string) types.Disks {
	filtered := types.Disks{}
	for _, disk := range disks {
		if disk.Location == memberName {
			filtered = append(filtered, disk)
		}
	}

	return filtered
}

// getUnpartitionedDisks fetches the list of available resources
func getUnpartitionedDisks(cli mcTypes.Client, memberName string) ([]Disk, error) {
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

	data, err := filterLocalDisks(resources, disks, memberName, nil)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// filterLocalDisks filters out the disks that are in use or otherwise not suitable for OSDs.
// It uses the shared FilterAvailableDisks function and converts to the Disk display type.
func filterLocalDisks(resources *api.ResourcesStorage, disks types.Disks, memberName string, cfg *common.DiskFilterConfig) ([]Disk, error) {
	availableDisks, err := common.FilterAvailableDisks(resources, disks, memberName, cfg)
	if err != nil {
		return nil, err
	}

	// Convert to display format
	data := make([]Disk, 0, len(availableDisks))
	for _, disk := range availableDisks {
		data = append(data, Disk{
			Model: disk.Model,
			Size:  units.GetByteSizeStringIEC(int64(disk.Size), 2),
			Type:  disk.Type,
			Path:  common.GetDevicePath(&disk),
		})
	}
	return data, nil
}
