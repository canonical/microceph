package main

import (
	"context"
	"encoding/json"
	"errors"
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
	flagOSDMatch   string
	flagWALMatch   string
	flagWALSize    string
	flagDBMatch    string
	flagDBSize     string
	flagDryRun     bool
	flagJSON       bool
}

func (c *cmdDiskAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <SPEC>",
		Short: "Add a new Ceph disk (OSD)",
		Long: `Adds one or more new Ceph disks (OSDs) to the cluster, alongside optional devices for write-ahead logging and database management.
The command takes arguments which is either one or more paths to block devices such as /dev/sdb, or a specification for loop files.

For block devices, add a space separated list of (absolute) paths, e.g. "/dev/sdb /dev/sdc ...". You may also specify external WAL and DB devices referred to by absolute paths. However when specifying WAL and DB devices you may only add a single OSD block device at a time.

The specification for loop files is of the form loop,<size>,<nr>

size is an integer with M, G, or T suffixes for megabytes, gigabytes, or terabytes.
nr is the number of file-backed loop OSDs to create.
For instance, a spec of loop,8G,3 will create 3 file-backed loop OSDs of 8GB each.

Note that loop files can't be used with encryption nor WAL/DB devices.

Alternatively, use --osd-match with a DSL expression to select devices based on attributes:
  microceph disk add --osd-match "eq(@type, 'nvme')"
  microceph disk add --osd-match "and(gt(@size, 100GiB), re('Samsung', @model))"

WAL/DB DSL selection is also supported:
  microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-encrypt --wal-wipe
  microceph disk add --osd-match "eq(@size, 11GiB)" --db-match "eq(@size, 30GiB)" --db-size 2GiB --db-encrypt --db-wipe
  microceph disk add --osd-match "eq(@size, 12GiB)" --encrypt --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-encrypt --db-match "eq(@size, 30GiB)" --db-size 2GiB --db-wipe

Available DSL predicates: and(), or(), not(), in(), re(), eq(), ne(), gt(), ge(), lt(), le()
Available variables: @type, @vendor, @model, @size, @devnode, @host`,
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
	cmd.PersistentFlags().StringVar(&c.flagOSDMatch, "osd-match", "", "DSL expression to match devices for OSD creation")
	cmd.PersistentFlags().StringVar(&c.flagWALMatch, "wal-match", "", "DSL expression to match backing devices for WAL partitions")
	cmd.PersistentFlags().StringVar(&c.flagWALSize, "wal-size", "", "Requested WAL partition size for --wal-match")
	cmd.PersistentFlags().StringVar(&c.flagDBMatch, "db-match", "", "DSL expression to match backing devices for DB partitions")
	cmd.PersistentFlags().StringVar(&c.flagDBSize, "db-size", "", "Requested DB partition size for --db-match")
	cmd.PersistentFlags().BoolVar(&c.flagDryRun, "dry-run", false, "Show matched devices without adding them (requires --osd-match)")
	cmd.PersistentFlags().BoolVar(&c.flagJSON, "json", false, "Provide dry-run output as a JSON-encoded DiskAddResponse.")

	return cmd
}

func (c *cmdDiskAdd) Run(cmd *cobra.Command, args []string) error {
	var req = types.DisksPost{}

	// Validate flag combinations
	if err := c.validateFlags(args); err != nil {
		return err
	}

	// No args passed and no match expression.
	if len(args) == 0 && !c.flagAllDevices && c.flagOSDMatch == "" {
		return cmd.Help()
	}

	err := c.validateBatchArgs(args)
	if err != nil {
		return fmt.Errorf("arg validation failed: %w", err)
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	if c.flagOSDMatch != "" {
		// DSL-based device selection
		req.OSDMatch = c.flagOSDMatch
		req.WALMatch = c.flagWALMatch
		req.WALSize = c.flagWALSize
		req.WALWipe = c.walWipe
		req.WALEncrypt = c.walEncrypt
		req.DBMatch = c.flagDBMatch
		req.DBSize = c.flagDBSize
		req.DBWipe = c.dbWipe
		req.DBEncrypt = c.dbEncrypt
		req.DryRun = c.flagDryRun
	} else if c.flagAllDevices {
		disks, err := getUnpartitionedDisks(cli)
		if err != nil {
			return err
		}

		if len(disks) == 0 {
			fmt.Println("No available disks found on this system.")
			return nil
		}

		// Prepare Batch arguments
		for _, disk := range disks {
			req.Path = append(req.Path, disk.Path)
		}
	} else {
		// Pass space separated params as disk paths.
		req.Path = args

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

	// required request params.
	req.Wipe = c.flagWipe
	req.Encrypt = c.flagEncrypt
	response, err := client.AddDisk(context.Background(), cli, &req)
	if err != nil {
		return err
	}

	// Handle dry-run output
	if c.flagDryRun {
		return c.printDryRunOutput(response)
	}

	for _, warning := range response.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}

	err = printAddDiskFailures(response)
	if err != nil {
		return err
	}

	return nil
}

// validateFlags checks for invalid flag combinations.
func (c *cmdDiskAdd) validateFlags(args []string) error {
	usesDSL := c.flagOSDMatch != "" || c.flagWALMatch != "" || c.flagDBMatch != ""

	// Any DSL matcher is mutually exclusive with positional args.
	if usesDSL && len(args) > 0 {
		return fmt.Errorf("--osd-match/--wal-match/--db-match cannot be used with positional device arguments")
	}

	// Any DSL matcher is mutually exclusive with --all-available.
	if usesDSL && c.flagAllDevices {
		return fmt.Errorf("--osd-match/--wal-match/--db-match cannot be used with --all-available")
	}

	// --dry-run requires --osd-match.
	if c.flagDryRun && c.flagOSDMatch == "" {
		return fmt.Errorf("--dry-run requires --osd-match")
	}
	if c.flagJSON && !c.flagDryRun {
		return fmt.Errorf("--json requires --dry-run")
	}

	// Legacy WAL/DB device flags remain unsupported with DSL mode.
	if usesDSL && (c.walDevice != "" || c.dbDevice != "") {
		return fmt.Errorf("--wal-device and --db-device are not supported with DSL matching in this version")
	}

	// Validate the WAL/DB matcher flag dependencies before sending the request.
	if c.flagWALMatch != "" && c.flagOSDMatch == "" {
		return fmt.Errorf("--wal-match requires --osd-match")
	}
	if c.flagDBMatch != "" && c.flagOSDMatch == "" {
		return fmt.Errorf("--db-match requires --osd-match")
	}
	if c.flagWALMatch != "" && c.flagWALSize == "" {
		return fmt.Errorf("--wal-match requires --wal-size")
	}
	if c.flagDBMatch != "" && c.flagDBSize == "" {
		return fmt.Errorf("--db-match requires --db-size")
	}
	if c.flagWALSize != "" && c.flagWALMatch == "" {
		return fmt.Errorf("--wal-size requires --wal-match")
	}
	if c.flagDBSize != "" && c.flagDBMatch == "" {
		return fmt.Errorf("--db-size requires --db-match")
	}
	if c.walEncrypt && c.flagWALMatch == "" && c.walDevice == "" {
		return fmt.Errorf("--wal-encrypt requires --wal-match or --wal-device")
	}
	if c.walWipe && c.flagWALMatch == "" && c.walDevice == "" {
		return fmt.Errorf("--wal-wipe requires --wal-match or --wal-device")
	}
	if c.dbEncrypt && c.flagDBMatch == "" && c.dbDevice == "" {
		return fmt.Errorf("--db-encrypt requires --db-match or --db-device")
	}
	if c.dbWipe && c.flagDBMatch == "" && c.dbDevice == "" {
		return fmt.Errorf("--db-wipe requires --db-match or --db-device")
	}

	return nil
}

// printDryRunOutput prints the dry-run results in a tabulated format.
func dryRunPartitionAction(plan *types.DryRunPartitionPlan) string {
	if plan == nil {
		return ""
	}
	if plan.ResetBeforeUse {
		return "reset"
	}
	if plan.Partition > 1 {
		return "append"
	}
	return "new"
}

func (c *cmdDiskAdd) printDryRunOutput(response types.DiskAddResponse) error {
	if c.flagJSON {
		err := printDryRunJSON(response)
		if err != nil {
			return err
		}
		if response.ValidationError != "" {
			return fmt.Errorf(response.ValidationError)
		}
		return nil
	}

	if response.ValidationError != "" {
		return fmt.Errorf(response.ValidationError)
	}

	for _, warning := range response.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}

	if len(response.DryRunPlan) > 0 {
		fmt.Println("Planned OSD/WAL/DB provisioning:")
		data := make([][]string, len(response.DryRunPlan))
		for i, plan := range response.DryRunPlan {
			walParent, walPart, walSize, walAction := "", "", "", ""
			if plan.WAL != nil {
				walParent = plan.WAL.ParentPath
				walPart = fmt.Sprintf("%d", plan.WAL.Partition)
				walSize = plan.WAL.Size
				walAction = dryRunPartitionAction(plan.WAL)
			}
			dbParent, dbPart, dbSize, dbAction := "", "", "", ""
			if plan.DB != nil {
				dbParent = plan.DB.ParentPath
				dbPart = fmt.Sprintf("%d", plan.DB.Partition)
				dbSize = plan.DB.Size
				dbAction = dryRunPartitionAction(plan.DB)
			}
			data[i] = []string{plan.OSDPath, walParent, walPart, walSize, dbParent, dbPart, dbSize, walAction, dbAction}
		}

		header := []string{"OSD", "WAL PARENT", "WAL PART#", "WAL SIZE", "DB PARENT", "DB PART#", "DB SIZE", "WAL ACTION", "DB ACTION"}
		sort.Sort(lxdCmd.SortColumnsNaturally(data))
		return lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, data)
	}

	if len(response.DryRunDevices) == 0 {
		fmt.Println("No devices matched the expression")
		return nil
	}

	fmt.Println("The following devices would be added as OSDs:")
	data := make([][]string, len(response.DryRunDevices))
	for i, dev := range response.DryRunDevices {
		data[i] = []string{dev.Path, dev.Model, dev.Size, dev.Type}
	}

	header := []string{"PATH", "MODEL", "SIZE", "TYPE"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))
	return lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, data)
}

func printDryRunJSON(response types.DiskAddResponse) error {
	output, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("internal error: unable to encode dry-run json output: %w", err)
	}

	fmt.Printf("%s\n", output)
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
		return errors.New(errStr)
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
