// Package types provides shared types and structs.
package types

type DisksPost struct {
	Path string `json:"path" yaml:"path"`
	Wipe bool   `json:"wipe" yaml:"wipe"`
}

type Disks []Disk
type Disk struct {
	OSD      int64  `json:"osd" yaml:"osd"`
	Path     string `json:"path" yaml:"path"`
	Location string `json:"location" yaml:"location"`
}
