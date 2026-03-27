// Package types provides shared types and structs.
package types

// DisksPost hold a path and a flag for enabling device wiping
type DisksPost struct {
	Path       []string `json:"path" yaml:"path"`
	Wipe       bool     `json:"wipe" yaml:"wipe"`
	Encrypt    bool     `json:"encrypt" yaml:"encrypt"`
	WALDev     *string  `json:"waldev" yaml:"waldev"`
	WALWipe    bool     `json:"walwipe" yaml:"walwipe"`
	WALEncrypt bool     `json:"walencrypt" yaml:"walencrypt"`
	DBDev      *string  `json:"dbdev" yaml:"dbdev"`
	DBWipe     bool     `json:"dbwipe" yaml:"dbwipe"`
	DBEncrypt  bool     `json:"dbencrypt" yaml:"dbencrypt"`
	// OSDMatch is a DSL expression for matching devices to use as OSDs.
	// When set, Path is ignored and devices are selected based on the expression.
	OSDMatch string `json:"osd_match,omitempty" yaml:"osd_match,omitempty"`
	// WALMatch is a DSL expression for matching backing devices for WAL partitions.
	// This is additive request plumbing for Phase 2 support.
	WALMatch string `json:"wal_match,omitempty" yaml:"wal_match,omitempty"`
	// WALSize is the requested WAL partition size.
	WALSize string `json:"wal_size,omitempty" yaml:"wal_size,omitempty"`
	// DBMatch is a DSL expression for matching backing devices for DB partitions.
	// This is additive request plumbing for Phase 2 support.
	DBMatch string `json:"db_match,omitempty" yaml:"db_match,omitempty"`
	// DBSize is the requested DB partition size.
	DBSize string `json:"db_size,omitempty" yaml:"db_size,omitempty"`
	// DryRun when true causes the command to report which devices would be
	// added without actually adding them. Only valid when OSDMatch is set.
	DryRun bool `json:"dry_run,omitempty" yaml:"dry_run,omitempty"`
}

// DiskAddReport holds report for single disk addition i.e. success/failure and optional error for failures.
type DiskAddReport struct {
	Path   string `json:"path" yaml:"path"`
	Report string `json:"report" yaml:"report"`
	Error  string `json:"error" yaml:"error"`
}

// DiskAddResponse holds response data for disk addition.
type DiskAddResponse struct {
	ValidationError string          `json:"validation_error" yaml:"validation_error"`
	Reports         []DiskAddReport `json:"report" yaml:"report"`
	Warnings        []string        `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	// DryRunDevices contains the list of devices that would be added
	// when dry_run is true. Only populated for OSD-only DSL-based requests.
	DryRunDevices []DryRunDevice `json:"dry_run_devices,omitempty" yaml:"dry_run_devices,omitempty"`
	// DryRunPlan contains the planned OSD->WAL/DB mapping for dry-run requests.
	DryRunPlan []DryRunOSDPlan `json:"dry_run_plan,omitempty" yaml:"dry_run_plan,omitempty"`
}

// DryRunDevice represents a device that would be added during an OSD-only dry run.
type DryRunDevice struct {
	Path   string `json:"path" yaml:"path"`
	Model  string `json:"model" yaml:"model"`
	Size   string `json:"size" yaml:"size"`
	Type   string `json:"type" yaml:"type"`
	Vendor string `json:"vendor" yaml:"vendor"`
}

// DryRunPartitionPlan represents one planned WAL or DB partition during dry-run.
type DryRunPartitionPlan struct {
	Kind           string `json:"kind" yaml:"kind"`
	ParentPath     string `json:"parent_path" yaml:"parent_path"`
	Partition      uint64 `json:"partition" yaml:"partition"`
	Size           string `json:"size" yaml:"size"`
	ResetBeforeUse bool   `json:"reset_before_use,omitempty" yaml:"reset_before_use,omitempty"`
}

// DryRunOSDPlan represents one planned OSD provision during dry-run.
type DryRunOSDPlan struct {
	OSDPath string               `json:"osd_path" yaml:"osd_path"`
	WAL     *DryRunPartitionPlan `json:"wal,omitempty" yaml:"wal,omitempty"`
	DB      *DryRunPartitionPlan `json:"db,omitempty" yaml:"db,omitempty"`
}

// DisksDelete holds an OSD number and a flag for forcing the removal
type DisksDelete struct {
	OSD                    int64 `json:"osdid" yaml:"osdid"`
	BypassSafety           bool  `json:"bypass_safety" yaml:"bypass_safety"`
	ConfirmDowngrade       bool  `json:"confirm_downgrade" yaml:"confirm_downgrade"`
	ProhibitCrushScaledown bool  `json:"prohibit_crush_scaledown" yaml:"prohibit_crush_scaledown"`
	Timeout                int64 `json:"timeout" yaml:"timeout"`
}

// Disks is a slice of disks
type Disks []Disk

// Disk holds data for a device: OSD number, it's path and a location
type Disk struct {
	OSD      int64  `json:"osd" yaml:"osd"`
	Path     string `json:"path" yaml:"path"`
	Location string `json:"location" yaml:"location"`
}

type DiskParameter struct {
	Path              string
	Encrypt           bool
	Wipe              bool
	LoopSize          uint64
	SkipPristineCheck bool
}
