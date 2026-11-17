package ceph

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRGWRecoveredBaselineSurvivesDatabaseFailure(t *testing.T) {
	for _, change := range []bool{false, true} {
		name := "identical"
		if change {
			name = "new certificate"
		}
		t.Run(name, func(t *testing.T) {
			defer setupRGWPaths(t)()
			rec := &rgwOpsRecorder{}
			defer rec.install(t)()
			si := newRGWTestState(t, errors.New("database unavailable"))
			a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
			deferRGWRotation(t, rec, a, b)
			bConf := readTestConf(t)
			request := b
			if change {
				request = c
			}
			sp := &RgwServicePlacement{}
			require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{
				"SSL": true, "SSLPort": request.sslPort,
				"SSLCertificate": base64.StdEncoding.EncodeToString(request.certPEM),
				"SSLPrivateKey":  base64.StdEncoding.EncodeToString(request.keyPEM),
			})))
			require.NoError(t, sp.ServiceInit(context.Background(), si))
			require.NoError(t, sp.PostPlacementCheck(si))
			assert.DirExists(t, a.tlsGenDir(), "recovery must not prune before the transaction")
			assert.DirExists(t, b.tlsGenDir())
			err := sp.DbUpdate(context.Background(), si)
			require.ErrorContains(t, err, "database unavailable", "an identical recovered request still attempts the transaction")
			assert.Equal(t, bConf, readTestConf(t))
			assert.DirExists(t, b.tlsGenDir())
			assert.NoDirExists(t, c.tlsGenDir())
			assert.NoFileExists(t, rgwPendingApplyPath())
			if change {
				assert.Equal(t, 3, rec.restarts, "recover B, activate C, restore B")
			} else {
				assert.Equal(t, 1, rec.restarts, "recovery alone establishes B as the baseline")
			}
		})
	}
}

func TestRGWTLSReuseRecoversBeforeReadingMaterial(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)
	a, b := testRGWSpec(t), testRGWSpec(t)
	aConf := deferRGWRotation(t, rec, a, b)
	require.NoError(t, os.Remove(filepath.Join(b.tlsGenDir(), "server.key")))
	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"SSL": true})))
	err := sp.ServiceInit(context.Background(), si)
	require.Error(t, err)
	assert.Equal(t, aConf, readTestConf(t), "recovery restores A before TLS reuse can fail on B's missing key")
	assert.True(t, rec.active)
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.DirExists(t, a.tlsGenDir())
}

func TestRGWCertificateDeferredRequestRecoversStoppedPendingFrontend(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)
	rec.active = false
	cert, key := base64.StdEncoding.EncodeToString(c.certPEM), base64.StdEncoding.EncodeToString(c.keyPEM)
	require.NoError(t, UpdateRGWCertificates(context.Background(), si, cert, key, false))
	assert.Equal(t, 2, rec.starts, "recover the stopped B before evaluating the running-TLS requirement")
	assert.Zero(t, rec.restarts, "C is still deferred")
	pending, err := readPendingApply()
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, bConf, *pending.PreviousConfig)
	assert.Contains(t, readTestConf(t), c.tlsGenDir())
	require.NoError(t, UpdateRGWCertificates(context.Background(), si, cert, key, false))
	assert.Equal(t, 1, rec.restarts, "an identical deferred request activates C once")
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.DirExists(t, c.tlsGenDir())
	assert.NoDirExists(t, a.tlsGenDir())
	assert.NoDirExists(t, b.tlsGenDir())
}
