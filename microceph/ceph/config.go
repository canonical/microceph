package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Config Table is the source of additional information for each supported config key
// Refer to GetConfigTable()
type ConfigTable map[string]struct {
	Who     string   // Ceph Config internal <who> against each key
	Daemons []string // List of Daemons that need to be restarted across the cluster for the config change to take effect.
}

// Check if certain key is present in the map.
func (c ConfigTable) isKeyPresent(key string) bool {
	if _, ok := c[key]; !ok {
		return false
	}

	return true
}

// Return keys of the given set
func (c ConfigTable) Keys() (keys []string) {
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// Since we can't have const maps, we encapsulate the map into a func
// so that each request for the map guarantees consistent definition.
func GetConstConfigTable() ConfigTable {
	return ConfigTable{
		"public_network":              {"global", []string{"mon", "osd"}},
		"cluster_network":             {"global", []string{"osd"}},
		"osd_pool_default_crush_rule": {"global", []string{}},
	}
}

func GetConfigTableServiceSet() Set {
	return Set{
		"mon": struct{}{},
		"mgr": struct{}{},
		"osd": struct{}{},
		"mds": struct{}{},
		"rgw": struct{}{},
	}
}

// Struct to get Config Items from config dump json output.
type ConfigDumpItem struct {
	Section string
	Name    string
	Value   string
}
type ConfigDump []ConfigDumpItem

func SetConfigItem(c types.Config) error {
	configTable := GetConstConfigTable()

	args := []string{
		"config",
		"set",
		configTable[c.Key].Who,
		c.Key,
		c.Value,
		"-f",
		"json-pretty",
	}

	_, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return err
	}

	return nil
}

func GetConfigItem(c types.Config) (types.Configs, error) {
	var err error
	configTable := GetConstConfigTable()
	ret := make(types.Configs, 1)
	who := "mon"

	// workaround to query global configs from mon entity
	if configTable[c.Key].Who != "global" {
		who = configTable[c.Key].Who
	}

	args := []string{
		"config",
		"get",
		who,
		c.Key,
	}

	ret[0].Key = c.Key
	ret[0].Value, err = processExec.RunCommand("ceph", args...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func RemoveConfigItem(c types.Config) error {
	configTable := GetConstConfigTable()
	args := []string{
		"config",
		"rm",
		configTable[c.Key].Who,
		c.Key,
	}

	_, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return err
	}

	return nil
}

func ListConfigs() (types.Configs, error) {
	var dump ConfigDump
	var configs types.Configs
	configTable := GetConstConfigTable()
	args := []string{
		"config",
		"dump",
		"-f",
		"json-pretty",
	}

	output, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return configs, err
	}

	json.Unmarshal([]byte(output), &dump)
	// Only take configs permitted in config table.
	for _, configItem := range dump {
		if configTable.isKeyPresent(configItem.Name) {
			configs = append(configs, types.Config{
				Key:   configItem.Name,
				Value: configItem.Value,
			})
		}
	}

	return configs, nil
}

// updates the ceph config file.
func updateConfig(s common.StateInterface) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(os.Getenv("SNAP_DATA"), "run")

	// Get the configuration and servers.
	var err error
	var configItems []database.ConfigItem
	var monitors []database.Service

	err = s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		configItems, err = database.GetConfigItems(ctx, tx)
		if err != nil {
			return err
		}

		serviceName := "mon"
		monitors, err = database.GetServices(ctx, tx, database.ServiceFilter{Service: &serviceName})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	config := map[string]string{}
	for _, item := range configItems {
		config[item.Key] = item.Value
	}

	monitorAddresses := make([]string, len(monitors))
	remotes := s.ClusterState().Remotes().RemotesByName()
	for i, monitor := range monitors {
		remote, ok := remotes[monitor.Member]
		if !ok {
			continue
		}

		monitorAddresses[i] = remote.Address.Addr().String()
	}

	conf := newCephConfig(confPath)
	address := s.ClusterState().Address().Hostname()
	err = conf.WriteConfig(
		map[string]any{
			"fsid":     config["fsid"],
			"runDir":   runPath,
			"monitors": strings.Join(monitorAddresses, ","),
			"addr":     address,
			"ipv4":     strings.Contains(address, "."),
			"ipv6":     strings.Contains(address, ":"),
		},
	)
	if err != nil {
		return fmt.Errorf("Couldn't render ceph.conf: %w", err)
	}

	// Generate ceph.client.admin.keyring
	keyring := newCephKeyring(confPath, "ceph.keyring")
	err = keyring.WriteConfig(
		map[string]any{
			"name": "client.admin",
			"key":  config["keyring.client.admin"],
		},
	)
	if err != nil {
		return fmt.Errorf("Couldn't render ceph.client.admin.keyring: %w", err)
	}

	return nil
}
