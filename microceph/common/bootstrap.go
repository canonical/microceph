// Package common package contains abstractions used by multiple other packages.
package common

import (
	"strconv"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

// BootstrapConfig holds all additional parameters that could be provided to the bootstrap API/CLI command.
// This structure is then consumed by the bootstrapper interface implementations to prepare specific
// parameters required for bootstrap.
type BootstrapConfig struct {
	// Simple Bootstrap Parameters
	MonIp  string // IP address of the monitor to be created.
	V2Only bool   // Whether only V2 addresses should be used.

	// ### Common Parameters
	PublicNet  string // Public Network subnet.
	ClusterNet string // Cluster Network subnet.

	// ### Adopt specific Parameters
	AdoptFSID     string   // fsid of the existing ceph cluster.
	AdoptMonHosts []string // slice of exisiting monitor addresses.
	AdoptAdminKey string   // Admin key for providing microceph with privileges.
}

func EncodeBootstrapConfig(data BootstrapConfig) map[string]string {
	logger.Debugf("encoding bootstrap config: %+v", data)

	return map[string]string{
		"MonIp":         data.MonIp,
		"PublicNet":     data.PublicNet,
		"ClusterNet":    data.ClusterNet,
		"V2Only":        strconv.FormatBool(data.V2Only),
		"AdoptFSID":     data.AdoptFSID,
		"AdoptMonHosts": strings.Join(data.AdoptMonHosts, ","),
		"AdoptAdminKey": data.AdoptAdminKey,
	}
}

func DecodeBootstrapConfig(input map[string]string, data *BootstrapConfig) {
	logger.Debugf("decoding bootstrap config: %+v", input)

	data.MonIp = input["MonIp"]
	data.PublicNet = input["PublicNet"]
	data.ClusterNet = input["ClusterNet"]
	data.V2Only, _ = strconv.ParseBool(input["V2Only"])
	data.AdoptFSID = input["AdoptFSID"]
	data.AdoptMonHosts = strings.Split(input["AdoptMonHosts"], ",")
	data.AdoptAdminKey = input["AdoptAdminKey"]
}
