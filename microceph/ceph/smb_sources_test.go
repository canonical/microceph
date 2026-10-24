package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
)

// smbEndpointNetwork supplies local inventory at the existing common.Network seam.
// The common package owns subnet-list parsing and host-interface enumeration.
type smbEndpointNetwork struct {
	common.NetworkIntf
	addresses []string
}

func (n smbEndpointNetwork) FindIpOnSubnet(subnets string) (string, error) {
	networks, err := common.ParseSubnetList(subnets)
	if err != nil {
		return "", err
	}
	for _, network := range networks {
		for _, address := range n.addresses {
			if network.Contains(net.ParseIP(address)) {
				return address, nil
			}
		}
	}
	return "", fmt.Errorf("no local match")
}

func (n smbEndpointNetwork) FindNetworkAddress(address string) (string, error) {
	for _, local := range n.addresses {
		if net.ParseIP(address).Equal(net.ParseIP(local)) {
			return local, nil
		}
	}
	return "", fmt.Errorf("not local")
}

func TestSMBEndpointPreparation(t *testing.T) {
	oldNetwork, oldFetch, oldPaths := common.Network, fetchConfigDb, constants.GetPathConst
	t.Cleanup(func() { common.Network, fetchConfigDb, constants.GetPathConst = oldNetwork, oldFetch, oldPaths })
	common.Network = smbEndpointNetwork{addresses: []string{"198.51.100.7", "2001:db8::7"}}
	publicNetwork := "198.51.100.0/24"
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": publicNetwork}, nil
	}
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "config"), 0700))
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: root} }
	require.NoError(t, os.WriteFile(filepath.Join(root, "ceph.conf"), []byte("[global]\n"), 0600))
	for _, tc := range []struct {
		name, fields, address string
		smb, metrics          int
		invalid               bool
	}{
		{"default", ``, "198.51.100.7", 445, 9922, false},
		{"default-v6", ``, "2001:db8::7", 445, 9922, false},
		{"default-missing", ``, "", 0, 0, true},
		{"default-no-match", ``, "", 0, 0, true},
		{"ipv4", `,"bind_addrs":[{"address":"198.51.100.7"}]`, "198.51.100.7", 445, 9922, false},
		{"ipv6", `,"bind_addrs":[{"address":"2001:0db8::7"}]`, "2001:db8::7", 445, 9922, false},
		{"ordered-mixed", `,"bind_addrs":[{"address":"192.0.2.9"},{"network":"2001:db8::/64"},{"network":"198.51.100.0/24"}]`, "2001:db8::7", 445, 9922, false},
		{"catchall", `,"bind_addrs":[{"network":"0.0.0.0/0"}]`, "198.51.100.7", 445, 9922, false},
		{"ipv6-catchall", `,"bind_addrs":[{"network":"::/0"}]`, "2001:db8::7", 445, 9922, false},
		{"network-host", `,"bind_addrs":[{"network":"2001:db8::7"}]`, "2001:db8::7", 445, 9922, false},
		{"nonlocal-literal", `,"bind_addrs":[{"address":"192.0.2.9"}]`, "", 0, 0, true},
		{"no-match", `,"bind_addrs":[{"network":"192.0.2.0/24"}]`, "", 0, 0, true},
		{"empty", `,"bind_addrs":[]`, "", 0, 0, true},
		{"xor", `,"bind_addrs":[{"address":"198.51.100.7","network":"198.51.100.0/24"}]`, "", 0, 0, true},
		{"missing", `,"bind_addrs":[{}]`, "", 0, 0, true},
		{"whole-list", `,"bind_addrs":[{"address":"198.51.100.7"},{"address":"invalid"}]`, "", 0, 0, true},
		{"unknown-selector", `,"bind_addrs":[{"address":"198.51.100.7","typo":true}]`, "", 0, 0, true},
		{"scope", `,"bind_addrs":[{"address":"fe80::7%eth0"}]`, "", 0, 0, true},
		{"unspecified-v4", `,"bind_addrs":[{"address":"0.0.0.0"}]`, "", 0, 0, true},
		{"unspecified-v6", `,"bind_addrs":[{"address":"::"}]`, "", 0, 0, true},
		{"multicast", `,"bind_addrs":[{"address":"ff02::1"}]`, "", 0, 0, true},
		{"host-bits", `,"bind_addrs":[{"network":"198.51.100.7/24"}]`, "", 0, 0, true},
		{"malformed-network", `,"bind_addrs":[{"network":"invalid/24"}]`, "", 0, 0, true},
		{"custom-ports", `,"custom_ports":{"smb":1445,"smbmetrics":19922,"ctdb":4379,"remote-control":54445}`, "198.51.100.7", 1445, 19922, false},
		{"port-boundaries", `,"custom_ports":{"smb":1,"smbmetrics":65535}`, "198.51.100.7", 1, 65535, false},
		{"zero", `,"custom_ports":{"smb":0}`, "", 0, 0, true},
		{"negative", `,"custom_ports":{"smbmetrics":-1}`, "", 0, 0, true},
		{"overflow", `,"custom_ports":{"smb":65536}`, "", 0, 0, true},
		{"type", `,"custom_ports":{"smb":"1445"}`, "", 0, 0, true},
		{"null-port", `,"custom_ports":{"smb":null}`, "", 0, 0, true},
		{"unknown-port", `,"custom_ports":{"other":1234}`, "", 0, 0, true},
		{"collision-default", `,"custom_ports":{"smb":9922}`, "", 0, 0, true},
		{"collision-custom", `,"custom_ports":{"smb":1445,"smbmetrics":1445}`, "", 0, 0, true},
		{"ctdb-fixed", `,"custom_ports":{"ctdb":14379}`, "", 0, 0, true},
		{"remote-deferred", `,"custom_ports":{"remote-control":54446}`, "", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publicNetwork = "198.51.100.0/24"
			switch tc.name {
			case "default-v6":
				publicNetwork = "2001:db8::/64"
			case "default-missing":
				publicNetwork = ""
			case "default-no-match":
				publicNetwork = "192.0.2.0/24"
			}
			oldRunner := common.ProcessExec
			common.ProcessExec = &smbStartupRunner{t: t, root: root, spec: `{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb"` + tc.fields + `}}`}
			t.Cleanup(func() { common.ProcessExec = oldRunner })
			candidate, err := readSMBPreparedCandidate(context.Background(), nil, "alpha", root, []byte(`{}`), false)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			raw, err := json.Marshal(candidate)
			require.NoError(t, err)
			var value map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &value))
			require.JSONEq(t, fmt.Sprintf(`{"frontend_address":%q,"smb_port":%d,"metrics_port":%d}`, tc.address, tc.smb, tc.metrics), string(value["endpoint"]))
		})
	}
}

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
