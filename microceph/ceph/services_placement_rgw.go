package ceph

import (
	"encoding/json"

	"github.com/canonical/microceph/microceph/common"
)

type RgwServicePlacement struct {
	Port int
}

func (rgw *RgwServicePlacement) PopulateParams(s common.StateInterface, payload string) error {

	err := json.Unmarshal([]byte(payload), &rgw)
	if err != nil {
		return err
	}

	return nil
}

func (rgw *RgwServicePlacement) HospitalityCheck(s common.StateInterface) error {
	return genericHospitalityCheck("rgw")
}

func (rgw *RgwServicePlacement) ServiceInit(s common.StateInterface) error {
	return EnableRGW(s, rgw.Port)
}

func (rgw *RgwServicePlacement) PostPlacementCheck(s common.StateInterface) error {
	return genericPostPlacementCheck("rgw")
}

func (rgw *RgwServicePlacement) DbUpdate(s common.StateInterface) error {
	return genericDbUpdate(s, "rgw")
}
