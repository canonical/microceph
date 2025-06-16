package ceph

import (
	"context"
	"encoding/json"

	"github.com/canonical/microceph/microceph/interfaces"
)

type NFSServicePlacement struct {
	ClusterID    string
	V4MinVersion uint
}

func (nfs *NFSServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), &nfs)
	if err != nil {
		return err
	}

	return nil
}

func (nfs *NFSServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return genericHospitalityCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	monitors, err := GetMonitorAddresses(ctx, s)
	if err != nil {
		return err
	}

	return EnableNFS(s, nfs.ClusterID, nfs.V4MinVersion, monitors)
}

func (nfs *NFSServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("nfs-ganesha")
}

func (nfs *NFSServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return genericDbUpdate(ctx, s, "nfs-ganesha")
}
