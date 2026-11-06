package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
)

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

func TestSMBInitialFingerprintHasCanonicalMonitorOrdering(t *testing.T) {
	configs := map[string]string{"mon.host.a": "192.0.2.3", "mon.host.b": "192.0.2.1", "mon.host.0": "192.0.2.2", "mon.host.gone": "192.0.2.4"}
	members := map[string]struct{}{"a": {}, "b": {}}
	for range 100 {
		require.Equal(t, []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}, getMonitorsFromConfig(configs, members))
	}
}

func TestSMBEndpointPreparation(t *testing.T) {
	oldNetwork, oldFetch := common.Network, fetchConfigDb
	t.Cleanup(func() { common.Network, fetchConfigDb = oldNetwork, oldFetch })
	common.Network = smbEndpointNetwork{addresses: []string{"198.51.100.7", "2001:db8::7"}}
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "198.51.100.0/24"}, nil
	}
	for _, test := range []struct {
		fields, address string
		bad             bool
	}{
		{``, "198.51.100.7", false},
		{`"bind_addrs":[{"address":"2001:db8::7"}]`, "2001:db8::7", false},
		{`"bind_addrs":[{"address":"192.0.2.9"},{"network":"198.51.100.0/24"}]`, "198.51.100.7", false},
		{`"bind_addrs":[]`, "", true},
		{`"bind_addrs":[{"address":"0.0.0.0"}]`, "", true},
		{`"bind_addrs":[{"network":"198.51.100.7/24"}]`, "", true},
		{`"bind_addrs":[{"address":"198.51.100.7","network":"198.51.100.0/24"}]`, "", true},
		{`"bind_addrs":[{"address":"198.51.100.7"},{"address":"invalid"}]`, "", true},
	} {
		var spec smbSpec
		require.NoError(t, json.Unmarshal([]byte(`{`+test.fields+`}`), &spec))
		endpoint, err := spec.resolveEndpoint(context.Background(), nil)
		if test.bad {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, test.address, endpoint.FrontendAddress)
		}
	}
	for _, port := range []int{1, 445, 65535} {
		endpoint, err := (smbSpec{CustomPorts: map[string]int{"smb": port}}).endpointPorts()
		require.NoError(t, err)
		require.Equal(t, port, endpoint.SMBPort)
	}
	for _, ports := range []map[string]int{{"smb": 0}, {"smb": 65536}, {"smbmetrics": 9922}, {"ctdb": 4379}, {"future": 1}} {
		_, err := (smbSpec{CustomPorts: ports}).endpointPorts()
		require.Error(t, err)
	}
}

func TestSMBSourcesFreshOverrideAndSecretBytes(t *testing.T) {
	store := map[string][]byte{
		"rados://.smb/alpha/spec.smb":                      []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb","user_sources":["rados:mon-config-key:smb/config/alpha/users.json"]}}`),
		"rados://.smb/alpha/config.smb":                    []byte(`{"configs":{"alpha":{"shares":[]}}}`),
		"rados:mon-config-key:smb/config/alpha/users.json": []byte(`{"users":{"all_entries":[{"name":"alice","password":"one"}]}}`),
	}
	read := func(uri string) ([]byte, error) {
		value, ok := store[uri]
		if !ok {
			return nil, errSMBSourceMissing
		}
		return value, nil
	}
	a, err := readSMBCandidate("alpha", read)
	require.NoError(t, err)
	store["rados:mon-config-key:smb/config/alpha/users.json"] = []byte(`{"password":"two"}`)
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
	_, err = readSMBCandidate("alpha", func(uri string) ([]byte, error) {
		if uri == "rados://.smb/alpha/spec.smb" {
			return store[uri], nil
		}
		return nil, errors.New("permission denied")
	})
	require.ErrorContains(t, err, "permission denied")
}

func TestSMBSourceURIAndLogicalIdentityBoundaries(t *testing.T) {
	for _, uri := range []string{"rados://.smb/other/config.smb", "rados://other/alpha/config.smb", "file:///etc/passwd", "rados://.smb/alpha/../secret", "rados://.smb/alpha/../beta", "rados://.smb/alpha/a?x", "rados:mon-config-key:smb/config/alpha/a/b", "rados:mon-config-key:smb/config/alphabeta/join.0.json", "https://host"} {
		require.Error(t, validateSMBSourceURI("alpha", uri))
	}
	for _, uri := range []string{"rados://.smb/alpha/config.smb", "rados:mon-config-key:smb/config/alpha/users.0.json", "rados:mon-config-key:smb/config/alpha/users-groups.0.json"} {
		require.NoError(t, validateSMBSourceURI("alpha", uri))
	}
	present, err := smbLogicalPresent("alpha", []byte(`{"resources":[]}`))
	require.NoError(t, err)
	require.False(t, present)
	_, err = smbLogicalPresent("alpha", []byte(`{"resource_type":"ceph.smb.cluster","cluster_id":"other"}`))
	require.Error(t, err)
}
