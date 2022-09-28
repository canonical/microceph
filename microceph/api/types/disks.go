// Package types provides shared types and structs.
package types

type DisksPost struct {
	Path string `json:"path" yaml:"path"`
	Wipe bool   `json:"wipe" yaml:"wipe"`
}
