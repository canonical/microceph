// Package types provides shared types and structs.
package types

// EnableRGWPost holds a flag for enabling RGW with a port number
type EnableRGWPost struct {
	Port int `json:"port" yaml:"port"`
}
