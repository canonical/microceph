// Package types provides shared types and structs.
package types

// Configs holds the key value pair
type Config struct {
	Key   string `json:"key" yaml:"key"`
	Value string `json:"value" yaml:"value"`
	Wait  bool   `json:"wait" yaml:"wait"`
}

// Configs is a slice of configs
type Configs []Config
