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

type ConfigExtras struct{
	Who     string // Ceph Config internal <who> against each key
	Daemons []string // Daemons that need to be restarted post config change.
	Regexp  string // Regular Expression to check value against key
	Cb      *func(s common.StateInterface, c types.Config)
}

type ConfigItem map[string]ConfigExtras

// Check if certain key is present in the map.
func (c ConfigItem) isKeyPresent(key string) bool {
	if _, ok := c[key]; !ok {
        return false
    }

	return true
}

// Return keys of the given set
func (c ConfigItem) Keys() (keys []string) {
    for k := range c {
        keys = append(keys, k)
    }
    return keys
}

func GetConfigTable() ConfigItem {
	return ConfigItem{
		"public_network":  {"global", []string{"mon", "osd"}, "", nil},
		"cluster_network": {"global", []string{"osd"}, "", nil},
	}
}
// Instantiation of config table.
var configTable = GetConfigTable()

// Struct to get Config Items from config dump json output.
type ConfigDump []struct{
	Section string
	Name    string
	Value   string
}

func RestartCephDaemon(daemon string) error {
	return snapReload(daemon)
}

func SetConfigItem(s common.StateInterface, c types.Config) error {
	args := []string{
		"config",
		"set",
		configTable[c.Key].Who,
		c.Key,
		c.Value,
	}

	_, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return err
	}

	return nil
}

func GetConfigItem(s common.StateInterface, c types.Config) (types.Config, error) {
	var err error
	args := []string{
		"config",
		"get",
		configTable[c.Key].Who,
		c.Key,
	}

	c.Value, err = processExec.RunCommand("ceph", args...)
	if err != nil {
		return c, err
	}

	return c, nil
}

func RemoveConfigItem(s common.StateInterface, c types.Config) error {
	args := []string{
		"config",
		"rm",
		configTable[c.Key].Who,
		c.Key,
		"-f",
		"json-pretty",
	}

	_, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return err
	}

	return nil
}

func ListConfigs(s common.StateInterface, key string) (types.Configs, error) {
	var dump ConfigDump
	var configs types.Configs
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
				Key: configItem.Name,
				Value:  configItem.Value,
			})
		}
	}

	return configs, nil
}

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
