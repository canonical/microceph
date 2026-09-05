package ceph

import (
	"context"
	"encoding/json"

	"github.com/canonical/microceph/microceph/interfaces"
)

type RgwServicePlacement struct {
	Port           int
	SSLPort        int
	SSLCertificate string
	SSLPrivateKey  string
}

func (rgw *RgwServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {

	err := json.Unmarshal([]byte(payload), &rgw)
	if err != nil {
		return err
	}

	return nil
}

func (rgw *RgwServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return genericHospitalityCheck("rgw")
}

func (rgw *RgwServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	monitorAddresses, err := GetMonitorAddresses(ctx, s)
	if err != nil {
		return err
	}

	return EnableRGW(s, rgw.Port, rgw.SSLPort, rgw.SSLCertificate, rgw.SSLPrivateKey, monitorAddresses)
}

func (rgw *RgwServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("rgw")
}

func (rgw *RgwServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return genericDbUpdate(ctx, s, "rgw")
}
