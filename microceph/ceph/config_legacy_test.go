package ceph

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/microceph/microceph/database"
)

func TestConfiguredMonitorAddressesNeedNoLegacyLookup(t *testing.T) {
	got, err := resolveMonitorAddresses(nil, map[string]string{"mon.host.first": "10.105.154.145"})
	if err != nil || !reflect.DeepEqual(got, []string{"10.105.154.145"}) {
		t.Fatalf("configured monitor addresses were not retained: %v %v", got, err)
	}
}

func TestLegacyMonitorAddresses(t *testing.T) {
	monitors := []database.Service{{Member: "first"}, {Member: "second"}}
	remotes := map[string]string{"first": "198.18.13.11", "second": "fd42::2"}
	got := legacyMonitorAddresses(monitors, remotes)
	want := []string{"198.18.13.11", "fd42::2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy monitor addresses: got %v, want %v", got, want)
	}
}

func TestCephConfigPreservesLegacyAndSubnetBindings(t *testing.T) {
	for _, subnet := range []string{"", "10.105.154.0/24"} {
		t.Run(map[bool]string{true: "legacy", false: "subnet"}[subnet == ""], func(t *testing.T) {
			config := newCephConfig(t.TempDir())
			err := config.WriteConfig(map[string]any{
				"fsid": "fixture", "runDir": "/var/snap/microceph/x2/run",
				"monitors": "198.18.13.11", "addr": "198.18.13.11",
				"pubNet": subnet, "ipv4": true, "ipv6": false,
			}, 0644)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(config.GetPath())
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "run dir = /var/snap/microceph/x2/run") {
				t.Fatal("configuration retained an obsolete revision directory")
			}
			if subnet == "" {
				if !strings.Contains(text, "public addr = 198.18.13.11") || strings.Contains(text, "public_network =") {
					t.Fatal("legacy per-member address binding was not preserved")
				}
			} else if !strings.Contains(text, "public_network = "+subnet) || strings.Contains(text, "public addr =") {
				t.Fatal("explicit public subnet binding was not preserved")
			}
		})
	}
}
