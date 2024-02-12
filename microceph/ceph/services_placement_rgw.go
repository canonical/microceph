package ceph

import (
	"encoding/json"
	"github.com/canonical/microceph/microceph/interfaces"
)

type RgwServicePlacement struct {
	Port int
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

func (rgw *RgwServicePlacement) ServiceInit(s interfaces.StateInterface) error {
	return EnableRGW(s, rgw.Port)
}

func (rgw *RgwServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("rgw")
}

func (rgw *RgwServicePlacement) DbUpdate(s interfaces.StateInterface) error {
	return genericDbUpdate(s, "rgw")
}
