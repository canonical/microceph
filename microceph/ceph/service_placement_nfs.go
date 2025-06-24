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

const (
	defaultAddress = "0.0.0.0:2049"
)

type NFSServicePlacement struct {
	ClusterID      string `json:"cluster_id"`
	V4MinVersion   uint   `json:"v4_min_version"`
	ServiceAddress string `json:"service_address"`
}

func (nfs *NFSServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), &nfs)
	if err != nil {
		return err
	}

	if len(nfs.ClusterID) == 0 {
		return fmt.Errorf("expected ClusterID to be non-empty")
	}

	if nfs.V4MinVersion > 2 {
		return fmt.Errorf("expected V4MinVersion to be in the interval [0, 2]")
	}

	if len(nfs.ServiceAddress) == 0 {
		nfs.ServiceAddress = defaultAddress
	}

	return nil
}

func (nfs *NFSServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	available, err := isAddressAvailable(nfs.ServiceAddress)
	if err != nil {
		return fmt.Errorf("error encountered during address availability check: %w", err)
	} else if !available {
		return fmt.Errorf("address '%s' is currently in use.", nfs.ServiceAddress)
	}

	return genericHospitalityCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	monitors, err := GetMonitorAddresses(ctx, s)
	if err != nil {
		return err
	}

	return EnableNFS(s, nfs.ClusterID, nfs.ServiceAddress, nfs.V4MinVersion, monitors)
}

func (nfs *NFSServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return genericDbUpdate(ctx, s, "nfs-ganesha")
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
