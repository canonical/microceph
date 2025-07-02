package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/canonical/microceph/microceph/interfaces"
)

type NFSServicePlacement struct {
	ClusterID    string `json:"cluster_id"`
	V4MinVersion uint   `json:"v4_min_version"`
	BindAddress  string `json:"bind_address"`
	BindPort     uint   `json:"bind_port"`
}

func (nfs *NFSServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), &nfs)
	if err != nil {
		return err
	}

	if len(nfs.ClusterID) == 0 {
		return fmt.Errorf("expected cluster_id to be non-empty")
	}

	if nfs.V4MinVersion > 2 {
		return fmt.Errorf("expected v4_min_version to be in the interval [0, 2]")
	}

	if len(nfs.BindAddress) == 0 {
		nfs.BindAddress = "0.0.0.0"
	} else {
		ip := net.ParseIP(nfs.BindAddress)
		if ip == nil {
			return fmt.Errorf("bind_address could not be parsed")
		}
	}

	if nfs.BindPort == 0 {
		nfs.BindPort = 2049
	} else if nfs.BindPort > 49151 {
		return fmt.Errorf("expected bind_port number to be in range [1-49151]")
	}

	return nil
}

func (nfs *NFSServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	address := fmt.Sprintf("%s:%d", nfs.BindAddress, nfs.BindPort)
	available, err := isAddressAvailable(address)
	if err != nil {
		return fmt.Errorf("error encountered during address availability check: %w", err)
	} else if !available {
		return fmt.Errorf("address '%s' is currently in use.", address)
	}

	return genericHospitalityCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	monitors, err := GetMonitorAddresses(ctx, s)
	if err != nil {
		return err
	}

	return EnableNFS(s, nfs.ClusterID, nfs.BindAddress, nfs.BindPort, nfs.V4MinVersion, monitors)
}

func (nfs *NFSServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	bytes, err := json.Marshal(map[string]any{
		"v4_min_version": nfs.V4MinVersion,
	})
	if err != nil {
		return err
	}
	config := string(bytes)

	bytes, err = json.Marshal(map[string]any{
		"bind_address": nfs.BindAddress,
		"bind_port":    nfs.BindPort,
	})
	if err != nil {
		return err
	}
	info := string(bytes)

	err = ensureNFSServiceGroupRecord(ctx, s, nfs.ClusterID, config)
	if err != nil {
		return nil
	}

	return createNFSServiceGroupRecord(ctx, s, nfs.ClusterID, info)
}

// isAddressAvailable checks if the given local address is available or not.
func isAddressAvailable(address string) (bool, error) {
	if l, err := net.Listen("tcp", address); errors.Is(err, syscall.EADDRINUSE) {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		l.Close()
		return true, nil
	}
}
