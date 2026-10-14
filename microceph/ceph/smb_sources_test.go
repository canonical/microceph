package ceph

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBSourcesFreshOverrideAndSecretBytes(t *testing.T) {
	store := map[string][]byte{
		"rados://.smb/alpha/spec.smb":                               []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb","user_sources":["rados:mon-config-key:smb/config/alpha/users-groups.0.json"]}}`),
		"rados://.smb/alpha/config.smb":                             []byte(`{"configs":{"alpha":{"shares":[]}}}`),
		"rados:mon-config-key:smb/config/alpha/users-groups.0.json": []byte(`{"users":{"all_entries":[{"name":"alice","password":"one"}]}}`),
	}
	read := func(uri string) ([]byte, error) {
		b, ok := store[uri]
		if !ok {
			return nil, errSMBSourceMissing
		}
		return b, nil
	}
	a, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	store["rados:mon-config-key:smb/config/alpha/users-groups.0.json"] = []byte(`{"users":{"all_entries":[{"name":"alice","password":"two"}]}}`)
	b, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	require.NotEqual(t, a.fingerprint(), b.fingerprint())
	store["rados://.smb/alpha/config.smb.override"] = []byte(`{"override":true}`)
	c, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	require.JSONEq(t, `{"override":true}`, string(c.Config))
	delete(store, "rados://.smb/alpha/config.smb.override")
	d, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	require.Equal(t, b.fingerprint(), d.fingerprint())
	delete(store, "rados:mon-config-key:smb/config/alpha/users-groups.0.json")
	_, err = readSMBCandidate("alpha", read)
	require.Error(t, err)
}

func TestSMBSourcesRejectForeignAndPublicAddresses(t *testing.T) {
	for _, spec := range []string{
		`{"cluster_id":"other","config_uri":"rados://.smb/alpha/config.smb"}`,
		`{"cluster_id":"alpha","config_uri":"rados://.smb/other/config.smb"}`,
		`{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb","cluster_public_addrs":[{"address":"192.0.2.1"}]}`,
	} {
		read := func(uri string) ([]byte, error) {
			if uri == "rados://.smb/alpha/spec.smb" {
				return []byte(`{"service_type":"smb","service_id":"alpha","spec":` + spec + `}`), nil
			}
			return []byte(`{}`), nil
		}
		_, err := readSMBCandidate("alpha", read)
		require.Error(t, err)
	}
	read := func(uri string) ([]byte, error) {
		if uri == "rados://.smb/alpha/spec.smb" {
			return []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb"}}`), nil
		}
		return nil, errors.New("permission denied")
	}
	_, err := readSMBCandidate("alpha", read)
	require.Error(t, err, "a failed override lookup is not absence")
}

func TestSMBLogicalDeletionRequiresExplicitAbsence(t *testing.T) {
	present, err := smbLogicalPresent("alpha", []byte(`{"resources":[]}`))
	require.NoError(t, err)
	require.False(t, present)
	present, err = smbLogicalPresent("alpha", []byte(`{"resource_type":"ceph.smb.cluster","cluster_id":"alpha"}`))
	require.NoError(t, err)
	require.True(t, present)
	for _, raw := range []string{`{}`, `{"resources":[{"cluster_id":"other"}]}`, `{"resource_type":"ceph.smb.cluster","cluster_id":"other"}`} {
		_, err = smbLogicalPresent("alpha", []byte(raw))
		require.Error(t, err)
	}
}

func TestSMBOverrideRemovalFallsBackBeforeSpecRegeneration(t *testing.T) {
	read := func(uri string) ([]byte, error) {
		switch uri {
		case "rados://.smb/alpha/spec.smb":
			return []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb.override"}}`), nil
		case "rados://.smb/alpha/config.smb":
			return []byte(`{"primary":true}`), nil
		default:
			return nil, errSMBSourceMissing
		}
	}
	c, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	require.JSONEq(t, `{"primary":true}`, string(c.Config))
}

func TestSMBLogicalPublicInputCannotUseAnOlderSpec(t *testing.T) {
	_, err := smbLogicalPresent("alpha", []byte(`{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","public_addrs":[{"address":"192.0.2.1"}]}`))
	require.ErrorContains(t, err, "separate HA/LB")
}
