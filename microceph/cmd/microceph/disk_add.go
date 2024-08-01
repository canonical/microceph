package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/constants"
)

type cmdDiskAdd struct {
	common *CmdControl
	disk   *cmdDisk

	flagWipe       bool
	flagEncrypt    bool
	walDevice      string
	walEncrypt     bool
	walWipe        bool
	dbDevice       string
	dbEncrypt      bool
	dbWipe         bool
	flagAllDevices bool
}

func (c *cmdDiskAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <SPEC>",
		Short: "Add a new Ceph disk (OSD)",
		Long: `Adds one or more new Ceph disks (OSDs) to the cluster, alongside optional devices for write-ahead logging and database management.
The command takes arguments which is either one or more paths to block devices such as /dev/sdb, or a specification for loop files.

For block devices, add a space separated list of paths, e.g. "/dev/sdb /dev/sdc ...". You may also add WAL and DB devices, but doing this is mutually exclusive with adding more than one OSD block device at a time.

The specification for loop files is of the form loop,<size>,<nr>

size is an integer with M, G, or T suffixes for megabytes, gigabytes, or terabytes.
nr is the number of file-backed loop OSDs to create.
For instance, a spec of loop,8G,3 will create 3 file-backed loop OSDs of 8GB each.

Note that loop files can't be used with encryption nor WAL/DB devices.`,
		RunE: c.Run,
	}

	cmd.PersistentFlags().BoolVar(&c.flagAllDevices, "all-available", false, "add all available devices as OSDs")
	cmd.PersistentFlags().BoolVar(&c.flagWipe, "wipe", false, "Wipe the disk prior to use")
	cmd.PersistentFlags().BoolVar(&c.flagEncrypt, "encrypt", false, "Encrypt the disk prior to use")
	cmd.PersistentFlags().StringVar(&c.walDevice, "wal-device", "", "The device used for WAL")
	cmd.PersistentFlags().BoolVar(&c.walWipe, "wal-wipe", false, "Wipe the WAL device prior to use")
	cmd.PersistentFlags().BoolVar(&c.walEncrypt, "wal-encrypt", false, "Encrypt the WAL device prior to use")
	cmd.PersistentFlags().StringVar(&c.dbDevice, "db-device", "", "The device used for the DB")
	cmd.PersistentFlags().BoolVar(&c.dbWipe, "db-wipe", false, "Wipe the DB device prior to use")
	cmd.PersistentFlags().BoolVar(&c.dbEncrypt, "db-encrypt", false, "Encrypt the DB device prior to use")

	return cmd
}

func (c *cmdDiskAdd) Run(cmd *cobra.Command, args []string) error {
	var req = types.DisksPost{}

	// No args passed.
	if len(args) == 0 && !c.flagAllDevices {
		return cmd.Help()
	}

	err := c.validateBatchArgs(args)
	if err != nil {
		return fmt.Errorf("arg validation failed: %w", err)
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	if c.flagAllDevices {
		disks, err := getUnpartitionedDisks(cli)
		if err != nil {
			return err
		}

		// Prepare Batch arguments
		for _, disk := range disks {
			req.Path = append(req.Path, disk.Path)
		}
	} else {
		// Pass space separated params as disk paths.
		req.Path = args

		if !strings.HasPrefix(req.Path[0], constants.LoopSpecId) {
			if c.walDevice != "" {
				req.WALDev = &c.walDevice
				req.WALWipe = c.walWipe
				req.WALEncrypt = c.walEncrypt
			}

			if c.dbDevice != "" {
				req.DBDev = &c.dbDevice
				req.DBWipe = c.dbWipe
				req.DBEncrypt = c.dbEncrypt
			}
		}
	}

	// required request params.
	req.Wipe = c.flagWipe
	req.Encrypt = c.flagEncrypt
	failures, err := client.AddDisk(context.Background(), cli, &req)
	if err != nil {
		return err
	}

	err = printAddDiskFailures(failures)
	if err != nil {
		return err
	}

	return nil
}

func printAddDiskFailures(response types.DiskAddResponse) error {
	var errStr string
	data := [][]string{}

	if len(response.ValidationError) != 0 {
		fmt.Println("Validation Error found")
		return fmt.Errorf(response.ValidationError)
	}

	if len(response.Reports) == 0 {
		// No responses; nothing to do.
		return nil
	}

	failureCount := 0
	for _, report := range response.Reports {
		if strings.Contains(report.Report, "Failure") {
			failureCount += 1
			errStr = report.Error
		}
		// prepare data for tabulated display.
		data = append(data, []string{report.Path, report.Report})
	}

	// Print disk add failures in tabulated form.
	fmt.Println("")
	header := []string{"Path", "Status"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))
	err := lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, data)
	if err != nil {
		return err
	}

	if failureCount == 1 {
		// Print error if only one instance of error is there.
		return fmt.Errorf(errStr)
	} else if failureCount > 1 {
		// Print error if only one instance of error is there.
		return fmt.Errorf("failed adding multiple (%d) disks, please check logs for details", failureCount)
	}

	return nil
}

// validateBatchArgs checks if no loop spec is provided as an argument to batch disk addition.
func (c *cmdDiskAdd) validateBatchArgs(args []string) error {
	// no validation if single arg is provided.
	if len(args) == 1 {
		return nil
	}

	// if wal/db devices are provided with batch commands.
	if c.walDevice != "" {
		return fmt.Errorf("--wal-device flag is not supported for batch disk addition")
	}

	if c.dbDevice != "" {
		return fmt.Errorf("--db-device flag is not supported for batch disk addition")
	}

	for _, diskPath := range args {
		if strings.HasPrefix(diskPath, constants.LoopSpecId) {
			return fmt.Errorf("loop spec %s is not supported as an argument to batch disk addition, use separately", diskPath)
		}
	}

	return nil
}
