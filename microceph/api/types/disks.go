// Package types provides shared types and structs.
package types

// DisksPost hold a path and a flag for enabling device wiping
type DisksPost struct {
	Path       string  `json:"path" yaml:"path"`
	Wipe       bool    `json:"wipe" yaml:"wipe"`
	Encrypt    bool    `json:"encrypt" yaml:"encrypt"`
	WALDev     *string `json:"waldev" yaml:"waldev"`
	WALWipe    bool    `json:"walwipe" yaml:"walwipe"`
	WALEncrypt bool    `json:"walencrypt" yaml:"walencrypt"`
	DBDev      *string `json:"dbdev" yaml:"dbdev"`
	DBWipe     bool    `json:"dbwipe" yaml:"dbwipe"`
	DBEncrypt  bool    `json:"dbencrypt" yaml:"dbencrypt"`
}

// DisksDelete holds an OSD number and a flag for forcing the removal
type DisksDelete struct {
	OSD              int64 `json:"osdid" yaml:"osdid"`
	BypassSafety     bool  `json:"bypass_safety" yaml:"bypass_safety"`
	ConfirmDowngrade bool  `json:"confirm_downgrade" yaml:"confirm_downgrade"`
	Timeout          int64 `json:"timeout" yaml:"timeout"`
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
	Path     string
	Encrypt  bool
	Wipe     bool
	LoopSize uint64
}
