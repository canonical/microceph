// Package common package contains abstractions used by multiple other packages.
package common

import (
	"strconv"
)

// BootstrapConfig holds all additional parameters that could be provided to the bootstrap API/CLI command.
// This structure is then consumed by the bootstrapper interface implementations to prepare specific
// parameters required for bootstrap.
type BootstrapConfig struct {
	MonIp      string // IP address of the monitor to be created.
	PublicNet  string // Public Network subnet.
	ClusterNet string // Cluster Network subnet.
	V2Only     bool   // Whether only V2 addresses should be used.
}

func EncodeBootstrapConfig(data BootstrapConfig) map[string]string {
	return map[string]string{
		"MonIp":      data.MonIp,
		"PublicNet":  data.PublicNet,
		"ClusterNet": data.ClusterNet,
		"V2Only":     strconv.FormatBool(data.V2Only),
	}
}

func DecodeBootstrapConfig(input map[string]string, data *BootstrapConfig) {
	data.MonIp = input["MonIp"]
	data.PublicNet = input["PublicNet"]
	data.ClusterNet = input["ClusterNet"]
	data.V2Only, _ = strconv.ParseBool(input["V2Only"])
}
