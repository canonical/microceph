package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// The record writers are injectable so the recorded values can be asserted
// without a dqlite statement registry.
var (
	createServiceRecordFunc = database.CreateService
	upsertRGWFrontendFunc   = database.UpsertRGWFrontend
)

// RgwServicePlacement applies RGW settings on one member.
// Placement sets SSL explicitly; legacy service requests infer it from material.
type RgwServicePlacement struct {
	Port           int
	SSLPort        int
	SSL            *bool
	SSLCertificate string
	SSLPrivateKey  string

	// effPort/effSSLPort are the effective listeners after validation and
	// defaulting; certPEM/keyPEM the decoded material (nil for plaintext or
	// when reusing local material); rollback is what ServiceInit published,
	// restored if a later pipeline phase fails. Set by PopulateParams /
	// ServiceInit; never serialized.
	effPort    int
	effSSLPort int
	certPEM    []byte
	keyPEM     []byte
	rollback   *rgwRollback
}

// PopulateParams validates the request before any files or services change.
func (rgw *RgwServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	// A JSON "null" (or empty object) unmarshals without touching the struct,
	// which would silently become a plaintext enable on the default port.
	// Require an object with at least one field.
	var fields map[string]json.RawMessage
	err := json.Unmarshal([]byte(payload), &fields)
	if err != nil || len(fields) == 0 {
		return fmt.Errorf("%w: rgw payload must be a JSON object with at least one field", ErrRgwFrontendInvalid)
	}
	err = json.Unmarshal([]byte(payload), rgw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRgwFrontendInvalid, err)
	}

	// Infer TLS intent when the caller omitted it (the enable rgw CLI):
	// supplied material means TLS, none means plaintext.
	if rgw.SSL == nil {
		ssl := rgw.SSLCertificate != "" || rgw.SSLPrivateKey != ""
		rgw.SSL = &ssl
	}

	// The placement contract owns the validation rules; apply them once here.
	enabled := true
	normalized, err := types.RgwPlacement{
		Enabled:        &enabled,
		SSL:            rgw.SSL,
		Port:           rgw.Port,
		SSLPort:        rgw.SSLPort,
		SSLCertificate: rgw.SSLCertificate,
		SSLPrivateKey:  rgw.SSLPrivateKey,
	}.Normalized()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRgwFrontendInvalid, err)
	}
	rgw.effPort = normalized.Port
	rgw.effSSLPort = normalized.SSLPort
	if rgw.SSLCertificate != "" {
		// Validated above; decode again for the bytes the member writes.
		rgw.certPEM, rgw.keyPEM, err = types.DecodeRGWCertificate(rgw.SSLCertificate, rgw.SSLPrivateKey)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRgwFrontendInvalid, err)
		}
	}
	// ssl=true without material means reuse: resolved against local state
	// in ServiceInit, which fails closed when no valid local pair exists.
	return nil
}

// HospitalityCheck permits an already-running gateway to be updated idempotently.
func (rgw *RgwServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return nil
}

// ServiceInit publishes the requested frontend and starts or updates the gateway.
func (rgw *RgwServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	config, err := getConfigDbFunc(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get config db: %w", err)
	}

	spec := rgwFrontendSpec{
		port:    rgw.effPort,
		sslPort: rgw.effSSLPort,
		ssl:     *rgw.SSL,
		certPEM: rgw.certPEM,
		keyPEM:  rgw.keyPEM,
	}
	if spec.ssl && spec.certPEM == nil {
		// ssl=true with no supplied material: reuse the local pair, or fail
		// closed — never fall back to plaintext.
		conf, err := readRGWConf()
		if err != nil {
			return err
		}
		spec.certPEM, spec.keyPEM, err = resolveRGWTLSReuse(conf)
		if err != nil {
			return err
		}
	}

	rollback, err := applyRGWFrontend(spec, getMonitorsFromConfig(config), true)
	if err != nil {
		return err
	}
	// Kept so PostPlacementCheck/DbUpdate can restore the previous usable
	// frontend if the pipeline fails after publication.
	rgw.rollback = rollback
	return nil
}

// PostPlacementCheck verifies the gateway is still active before recording it.
func (rgw *RgwServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	err := checkRGWActiveFunc()
	if err != nil {
		return fmt.Errorf("%w: RGW did not stay active: %w", ErrPlacementOperationFailed, errors.Join(err, rgw.rollback.restore()))
	}
	return nil
}

// DbUpdate records the confirmed frontend and finishes its pending publication.
func (rgw *RgwServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	if s.ClusterState().ServerCert() == nil {
		// No server certificate means this member never joined the cluster's
		// trust store, so there is no per-member identity for the database
		// write below to record anything against.
		// rollback is nil when nothing was published, so join rather than
		// wrap: a nil operand would render as "%!w(<nil>)".
		return fmt.Errorf("%w: no server certificate: %w", ErrPlacementOperationFailed,
			errors.Join(errors.New("member has no server certificate"), rgw.rollback.restore()))
	}
	member := s.ClusterState().Name()

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Idempotent services row: an already-present (member, rgw) row is the
		// success case for a re-apply, not an error.
		_, err := createServiceRecordFunc(ctx, tx, database.Service{Member: member, Service: "rgw"})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("failed to record rgw service: %w", err)
		}

		// Record the observed frontend (effective ports + TLS flag only,
		// never key material) in the same transaction as the services row, so
		// presence and frontend stay consistent and GET /placement can report
		// the last successfully applied state from dqlite.
		err = upsertRGWFrontendFunc(ctx, tx, member, rgw.effPort, rgw.effSSLPort, *rgw.SSL)
		if err != nil {
			return fmt.Errorf("failed to record rgw frontend: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: failed to record RGW state: %w", ErrPlacementOperationFailed, errors.Join(err, rgw.rollback.restore()))
	}

	// The apply is complete once the record is committed, so a journal that
	// cannot be cleared is not an apply failure. It is re-pointed at the
	// configuration just confirmed so a later rollback lands here rather
	// than on the state this apply replaced.
	err = finishRGWApply()
	if err != nil {
		logger.Warnf("RGW apply recorded but not finalised: %v", err)
		refreshPendingApplyMarker()
	}
	return nil
}
