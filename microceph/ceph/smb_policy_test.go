package ceph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBLocalPolicyRejectsFutureModes(t *testing.T) {
	for _, feature := range []string{"clustered", "domain", "cephfs-proxy", "future"} {
		t.Run(feature, func(t *testing.T) {
			candidate := smbCandidate{Spec: smbSpec{ClusterID: "alpha", Features: []string{feature}}}
			require.Error(t, validateSMBPolicyDescription(candidate, smbBindingDescription{}))
			require.Error(t, validateSMBLogicalPolicy([]byte(`{"auth_mode":"user","clustering":"never"}`), candidate.Spec))
		})
	}
	for _, logical := range []string{`{"auth_mode":"active-directory"}`, `{"clustering":"always"}`, `{"auth_mode":"future"}`, `invalid`} {
		require.Error(t, validateSMBLogicalPolicy([]byte(logical), smbSpec{ClusterID: "alpha"}))
	}
}

func TestSMBLocalDescriptionPreservesLogicalAuthenticationConsistency(t *testing.T) {
	for _, logical := range []string{`{"auth_mode":"user"}`, `{}`, ``} {
		require.NoError(t, validateSMBPolicyDescription(smbCandidate{Logical: json.RawMessage(logical)}, smbBindingDescription{}))
	}
	for _, logical := range []string{`{"auth_mode":"active-directory"}`, `{"auth_mode":"future"}`, `invalid`} {
		require.Error(t, validateSMBPolicyDescription(smbCandidate{Logical: json.RawMessage(logical)}, smbBindingDescription{}))
	}
}

func TestSMBLocalEffectivePolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		options [][2]string
		valid   bool
	}{
		{"empty", nil, true},
		{"user", [][2]string{{"security", "user"}}, true},
		{"auto", [][2]string{{"security", "AUTO"}}, true},
		{"ads", [][2]string{{"security", "ADS"}}, false},
		{"domain", [][2]string{{"security", "domain"}}, false},
		{"realm", [][2]string{{"realm", "EXAMPLE.TEST"}}, false},
		{"empty realm", [][2]string{{"realm", ""}}, true},
		{"controller", [][2]string{{"server role", "active directory domain controller"}}, false},
		{"member", [][2]string{{"serverrole", "member server"}}, false},
		{"standalone", [][2]string{{"SERVER\tROLE", "standalone server"}}, true},
		{"derived controller", [][2]string{{"domain logons", "yes"}}, false},
		{"invalid logons", [][2]string{{"domain logons", "invalid"}}, false},
		{"standalone ignores logons", [][2]string{{"server role", "standalone"}, {"domain logons", "yes"}}, true},
		{"clustering", [][2]string{{"clustering", "on"}}, false},
		{"no clustering", [][2]string{{"clustering", "off"}}, true},
		{"invalid clustering", [][2]string{{"clustering", "invalid"}}, false},
		{"last wins", [][2]string{{"security", "ads"}, {"SEC URITY", "user"}}, true},
		{"last wins domain", [][2]string{{"security", "user"}, {"SEC URITY", "ads"}}, false},
		{"last wins role", [][2]string{{"server role", "member"}, {"SERVERROLE", " standalone server "}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateSMBPolicyDescription(smbCandidate{}, smbBindingDescription{Globals: test.options})
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, description := range []smbBindingDescription{{Clustered: true}, {DomainController: true}} {
		require.Error(t, validateSMBPolicyDescription(smbCandidate{}, description))
	}
}

func TestSMBInspectionPolicyRejectsEffectiveProxy(t *testing.T) {
	description := smbBindingDescription{}
	require.NoError(t, json.Unmarshal([]byte(`{"shares":[{"name":"data","options":[["vfs objects","ceph_new"],["CEPH_NEW:PROXY","yes"]]}]}`), &description))
	require.Error(t, validateSMBPolicyDescription(smbCandidate{}, description), "pre-reservation inspection must reject proxy, not just projection after reservation")
}

func TestSMBLocalSpecRejectsUnsupportedSources(t *testing.T) {
	for _, extra := range []string{`"join_sources":["secret"]`, `"custom_dns":["192.0.2.1"]`, `"remote_control_ssl_cert":"secret"`, `"features":["clustered"]`, `"cluster_public_addrs":[{}]`} {
		_, err := decodeSMBSpec("alpha", []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha",`+extra+`}}`))
		require.Error(t, err)
	}
}

func TestSMBStartupPlanRejectsFutureRoleBeforeAnyEffects(t *testing.T) {
	for _, role := range []string{"winbindd", "smbmetrics", "cephfs-proxy", "future"} {
		calls := 0
		ops := smbAppOps{
			prepare: func(_ context.Context, _ string) error { calls++; return nil },
			start:   func(_ context.Context, _ string) error { calls++; return nil },
		}
		require.Error(t, startSMBApps(context.Background(), t.TempDir(), []string{"smbd", role}, ops))
		require.Zero(t, calls)
	}
}
