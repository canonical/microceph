// Package types provides shared types and structs.
package types

// OsdPut holds data structure for updating the state of osd service
type OsdPut struct {
	State string `json:"state" yaml:"state"`
}
