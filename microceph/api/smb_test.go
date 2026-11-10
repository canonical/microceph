package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestSMBOrchRetirementReturnsBeforeNativeWork(t *testing.T) {
	old := retireSMBOrchestratedFunc
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	retireSMBOrchestratedFunc = func(ctx context.Context, _ interfaces.StateInterface, _ string) error {
		defer close(done)
		started <- ctx
		<-release
		return nil
	}
	t.Cleanup(func() {
		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("retirement worker did not finish")
		}
		retireSMBOrchestratedFunc = old
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("DELETE", "/1.0/services/smb", strings.NewReader(`{"cluster_id":"alpha","orchestrated":true}`)).WithContext(ctx)
	returned := make(chan struct{}, 1)
	go func() {
		cmdSMBDelete(nil, request)
		returned <- struct{}{}
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("orch callback blocked on native retirement")
	}
	cancel()
	select {
	case workCtx := <-started:
		require.NoError(t, workCtx.Err(), "accepted work must outlive the HTTP request")
	case <-time.After(time.Second):
		t.Fatal("retirement was not dispatched")
	}
}

func TestSMBUsesExistingServiceFamily(t *testing.T) {
	found := false
	for _, r := range Servers["microceph"].Resources {
		for _, e := range r.Endpoints {
			if e.Path == "services/smb" {
				found = true
				require.NotNil(t, e.Get.Handler)
				require.NotNil(t, e.Put.Handler)
				require.NotNil(t, e.Delete.Handler)
				require.True(t, e.Put.ProxyTarget)
			}
			require.NotEqual(t, "managed-services", e.Path)
		}
	}
	require.True(t, found)
}

func TestSMBGetConsumesPreparedADStatusWithoutCredentials(t *testing.T) {
	common := t.TempDir()
	t.Setenv("SNAP_COMMON", common)
	base := filepath.Join(common, "smb")
	root := filepath.Join(base, "alpha")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(base, "owner.json"), []byte("alpha"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stop-requested"), []byte("stopped"), 0600))
	launch := `{"clustered":true,"ctdb_pnn":0,"ad_member":{"realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","netbios_name":"ALPHA","fqdn":"alpha.example.test"}}`
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/context.json"), []byte(launch), 0600))
	request := httptest.NewRequest("GET", "/1.0/services/smb", nil)
	response := cmdSMBGet(nil, request)
	writer := httptest.NewRecorder()
	require.NoError(t, response.Render(writer, request))
	require.Equal(t, 200, writer.Code)
	var envelope struct {
		Metadata types.SMBStatus `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &envelope))
	require.Equal(t, "active-directory", envelope.Metadata.AuthMode)
	require.Equal(t, "EXAMPLE.TEST", envelope.Metadata.Realm)
	require.True(t, envelope.Metadata.StopRequested)
	require.False(t, envelope.Metadata.Ready)
	require.NotContains(t, writer.Body.String(), "password")
}
