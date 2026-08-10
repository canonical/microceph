package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/database"
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
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	member := s.ClusterState().Name()
	// Record the ports that were actually applied (after the default-port-80
	// rule) so the observed frontend matches what is running, not the raw input.
	effPort, effSSLPort := effectiveRGWPorts(rgw.Port, rgw.SSLPort, rgw.SSLCertificate, rgw.SSLPrivateKey)
	ssl := rgw.SSLCertificate != "" && rgw.SSLPrivateKey != ""

	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Idempotent services row: ignore an already-present (member, rgw) row so
		// re-applying placement to an enabled member does not error on a
		// duplicate insert. CreateService returns http.StatusConflict (409) when
		// the row exists; that is the success case for a re-apply.
		_, err := database.CreateService(ctx, tx, database.Service{Member: member, Service: "rgw"})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("failed to record rgw service: %w", err)
		}

		// Record the observed frontend (ports + TLS flag) in the same transaction
		// as the services row, so presence and frontend config stay consistent
		// and GET /placement can report them from dqlite. Stores ports + flag
		// only, never cert/key bytes.
		err = database.UpsertRGWFrontend(ctx, tx, member, effPort, effSSLPort, ssl)
		if err != nil {
			return fmt.Errorf("failed to record rgw frontend: %w", err)
		}
		return nil
	})
}
