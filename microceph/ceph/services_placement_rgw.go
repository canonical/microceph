package ceph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type RgwServicePlacement struct {
	Port           int    `json:"port"`
	SSLPort        int    `json:"ssl_port"`
	SSLCertificate string `json:"ssl_certificate"`
	SSLPrivateKey  string `json:"ssl_private_key"`
	GroupID        string `json:"group_id"`
}

func (rgw *RgwServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), &rgw)
	if err != nil {
		return err
	}

	// Validate GroupID if provided (optional for backward compatibility)
	if rgw.GroupID != "" {
		if !types.NFSClusterIDRegex.MatchString(rgw.GroupID) {
			return fmt.Errorf("expected group_id to be valid (regex: '%s')", types.NFSClusterIDRegex.String())
		}
	}

	return nil
}

func (rgw *RgwServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return genericHospitalityCheck("rgw")
}

func (rgw *RgwServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	// fetch configs from db
	config, err := GetConfigDb(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get config db: %w", err)
	}

	return EnableRGW(s, rgw.Port, rgw.SSLPort, rgw.SSLCertificate, rgw.SSLPrivateKey, getMonitorsFromConfig(config))
}

func (rgw *RgwServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck("rgw")
}

func (rgw *RgwServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	// If GroupID is provided, use grouped service model
	if rgw.GroupID != "" {
		groupConfig := database.RGWServiceGroupConfig{}
		serviceInfo := database.RGWServiceInfo{
			Port:           rgw.Port,
			SSLPort:        rgw.SSLPort,
			SSLCertificate: rgw.SSLCertificate,
			SSLPrivateKey:  rgw.SSLPrivateKey,
		}

		return database.GroupedServicesQuery.AddNew(ctx, s, "rgw", rgw.GroupID, groupConfig, serviceInfo)
	}

	// Fall back to ungrouped service for backward compatibility
	return genericDbUpdate(ctx, s, "rgw")
}
