// Package types provides shared types and structs.
package types

// holds the name, access and secretkey required for exposing an S3 user.
type S3User struct {
	Name     string `json:"name" yaml:"name"`
	Key      string `json:"key" yaml:"key"`
	Secret   string `json:"secret" yaml:"secret"`
}