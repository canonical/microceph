package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// Bootstrapper encapsulates the bootstrap
type Bootstrapper interface {
	Prefill(bd common.BootstrapConfig, state interfaces.StateInterface) error
	Precheck(ctx context.Context, state interfaces.StateInterface) error
	Bootstrap(ctx context.Context, state interfaces.StateInterface) error
}

// getBootstrapper returns a bootstrapper implementation based on the bootstrap parameters.
var getBootstrapper = func(bd common.BootstrapConfig, state interfaces.StateInterface) (Bootstrapper, error) {
	var bootstrapper Bootstrapper

	if len(bd.AdoptFSID) != 0 && len(bd.AdoptMonHosts) != 0 && len(bd.AdoptAdminKey) != 0 {
		logger.Debugf("Adopt ceph cluster with %+v", bd)
		bootstrapper = &AdoptBootstrapper{}
	} else {
		logger.Debugf("Simple bootstrap with %+v", bd)
		bootstrapper = &SimpleBootstrapper{}
	}

	// Perform Prefill
	err := bootstrapper.Prefill(bd, state)
	if err != nil {
		logger.Errorf("failed to prefill simple bootstrapper: %v", err)
		return nil, err
	}

	return bootstrapper, nil
}

// PopulateDefaultNetworkParams provides missing network parameters with default values.
func PopulateDefaultNetworkParams(state interfaces.StateInterface, monIP *string, publicNet *string, clusterNet *string) error {
	var err error

	logger.Debugf("Provided Bootstrap params: mon ip (%s), public net (%s), cluster net (%s)", *monIP, *publicNet, *clusterNet)

	if len(*monIP) == 0 { // Initialise mon-ip if not provided.
		if len(*publicNet) == 0 {
			// Use default value if public address is also not provided.
			*monIP = state.ClusterState().Address().Hostname()
			logger.Debugf("mon ip and public network missing, using default value as %s", *monIP)
		} else {
			// deduce mon-ip from the public network parameter.
			*monIP, err = common.Network.FindIpOnSubnet(*publicNet)
			if err != nil {
				logger.Errorf("failed to deduce mon ip from public network %s: %v", *publicNet, err)
				return err
			}
			logger.Debugf("mon ip missing, deduced from public network %s as %s", *publicNet, *monIP)
		}
	}

	// Initialise public network if not provided.
	if len(*publicNet) == 0 {
		*publicNet, err = common.Network.FindNetworkAddress(*monIP)
		if err != nil {
			err = fmt.Errorf("failed to locate %s on host: %v", *monIP, err)
			logger.Error(err.Error())
			return err
		}
	}

	// Initialise cluster network if not provided.
	if len(*clusterNet) == 0 {
		*clusterNet = *publicNet
		logger.Debugf("No cluster network provided, defaulting to public network (%s)", *publicNet)
	}

	return nil
}

// PopulateV2OnlyMonIP adds v2 protocol strictly
func PopulateV2OnlyMonIP(monIP *string, v2Only bool) {
	if v2Only {
		*monIP = constants.V2OnlyMonIPProtoPrefix + *monIP + constants.V2OnlyMonIPPort
		logger.Debugf("mon v2 only is set, using mon ip as %s", *monIP)
	}
}

func StripV2OnlyMonIP(monIP string) string {
	if strings.Contains(monIP, constants.V2OnlyMonIPProtoPrefix) {
		monIP = strings.ReplaceAll(monIP, constants.V2OnlyMonIPProtoPrefix, "")
		monIP = strings.ReplaceAll(monIP, constants.V2OnlyMonIPPort, "")
	}

	return monIP
}

// ##### Validation methods for various members of the bootstrap config structure #####

// ValidateNetworkParams validates network parameters for bootstrap and assign default values if needed.
func ValidateNetworkParams(state interfaces.StateInterface, monIP *string, publicNet *string, clusterNet *string) error {
	var err error

	// check if mon IP available on host
	_, err = common.Network.FindNetworkAddress(*monIP)
	if err != nil {
		err = fmt.Errorf("failed to locate monIP %s on host: %v", *monIP, err)
		logger.Error(err.Error())
		return err
	}

	// check if mon ip and public network compatible
	if !common.Network.IsIpOnSubnet(*monIP, *publicNet) {
		err := fmt.Errorf("monIP %s is not available on public network %s", *monIP, *publicNet)
		logger.Error(err.Error())
		return err
	}

	// check if cluster network is available on host interfaces
	_, err = common.Network.FindIpOnSubnet(*clusterNet)
	if err != nil {
		logger.Errorf("failed to locate cluster network %s on host: %v", *clusterNet, err)
		return err
	}

	logger.Debugf("Succesfully validated mon-ip(%s), public_network(%s) and cluster_network(%s)", *monIP, *publicNet, *clusterNet)

	return nil
}

// ValidateMonV2Param validates the mon v2 only parameter
func ValidateMonV2Param(state interfaces.StateInterface, monIP *string, v2Only bool) error {
	if v2Only {
		if !strings.HasPrefix(*monIP, constants.V2OnlyMonIPProtoPrefix) || !strings.HasSuffix(*monIP, constants.V2OnlyMonIPPort) {
			err := fmt.Errorf("mon v2 only is set but mon ip %s does not have v2 protocol strictly", *monIP)
			logger.Error(err.Error())
			return err
		}
	}

	return nil
}
