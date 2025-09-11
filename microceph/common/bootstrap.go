package common

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// BootstrapConfig holds all additional parameters that could be provided to the bootstrap API/CLI command.
// This structure is then consumed by the bootstraper interface implementations to prepare specific
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

// ##### Validation methods for various members of the bootstrap config structure #####

// ValidateNetworkParams validates network parameters for bootstrap and assign default values if needed.
func ValidateNetworkParams(state interfaces.StateInterface, monIP *string, publicNet *string, clusterNet *string) error {
	var err error
	// if no mon-ip is provided, either deduce from public network or fallback to default.
	if len(*monIP) == 0 {
		if len(*publicNet) == 0 {
			// Use default value if public address is also not provided.
			*monIP = state.ClusterState().Address().Hostname()
		} else {
			// deduce mon-ip from the public network parameter.
			*monIP, err = Network.FindIpOnSubnet(*publicNet)
			if err != nil {
				return fmt.Errorf("failed to locate %s on host: %w", *monIP, err)
			}
		}
		logger.Debugf("No mon ip provided, using default value as %s", *monIP)
	}

	// at this point mon ip is non empty.
	if len(*publicNet) != 0 {
		// Verify that the public network and mon-ip params are coherent.
		if !Network.IsIpOnSubnet(*monIP, *publicNet) {
			return fmt.Errorf("monIP %s is not available on public network %s", *monIP, *publicNet)
		}
		logger.Debugf("mon ip %s is compliant with public network %s", *monIP, *publicNet)
	} else {
		// Deduce Public network based on mon-ip param.
		*publicNet, err = Network.FindNetworkAddress(*monIP)
		if err != nil {
			return fmt.Errorf("failed to locate %s on host: %w", *monIP, err)
		}
		logger.Debugf("No public network provided, defaulting to mon ip (%s) subnet value as %s", *monIP, *publicNet)
	}

	if len(*clusterNet) == 0 {
		// Cluster Network defaults to Public Network.
		*clusterNet = *publicNet
		logger.Debugf("No cluster network provided, defaulting to public network (%s)", *publicNet)
	}

	// Ensure mon-ip is enclosed in square brackets if IPv6.
	if net.ParseIP(*monIP) != nil && strings.Contains(*monIP, ":") {
		*monIP = fmt.Sprintf("[%s]", *monIP)
		logger.Debugf("mon ip is ipv6, enclosing in square brackets: %s", *monIP)
	}

	return nil
}

// ValidateMonV2Param validates the mon v2 only parameter and adjusts the mon-ip.
func ValidateMonV2Param(state interfaces.StateInterface, monIP *string, v2Only bool) error {
	// If v2 only addressing is used, append v2 protocol and port to the address.
	if v2Only {
		*monIP = "v2:" + *monIP + ":3300"
		logger.Debugf("mon v2 only is set, using mon ip as %s", *monIP)
	}

	return nil
}
