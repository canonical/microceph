package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

type ClusterConfigPermission string

const (
	ClusterConfigRO ClusterConfigPermission = "read_only"
	ClusterConfigRW ClusterConfigPermission = "read_write"
)

type ClusterConfigDefinition struct {
	Who        string                  // Ceph Config internal <who> against each key
	Permission ClusterConfigPermission // read only or read write
	Daemons    []string                // List of Daemons that need to be restarted across the cluster for the config change to take effect.
}

// Config Table is the source of additional information for each supported config key
// Refer to GetConfigTable()
type ConfigTable map[string]ClusterConfigDefinition

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
		// Cluster config keys
		"public_network":              {"global", ClusterConfigRO, []string{"osd"}},
		"cluster_network":             {"global", ClusterConfigRW, []string{"osd"}},
		"osd_pool_default_crush_rule": {"global", ClusterConfigRW, []string{}},
		// RGW config keys
		"rgw_s3_auth_use_keystone":                    {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_url":                            {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_token":                    {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_token_path":               {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_user":                     {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_password":                 {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_password_path":            {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_project":                  {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_admin_domain":                   {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_service_token_enabled":          {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_service_token_accepted_roles":   {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_expired_token_cache_expiration": {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_api_version":                    {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_accepted_roles":                 {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_accepted_admin_roles":           {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_token_cache_size":               {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_verify_ssl":                     {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_keystone_implicit_tenants":               {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_swift_account_in_url":                    {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_swift_versioning_enabled":                {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_swift_enforce_content_length":            {"global", ClusterConfigRW, []string{"rgw"}},
		"rgw_swift_custom_header":                     {"global", ClusterConfigRW, []string{"rgw"}},
	}
}

func GetConfigTableServiceSet() common.Set {
	return common.Set{
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
	// safety checks
	canSet, err := canSetConfig(c.Key)
	if !canSet {
		return fmt.Errorf("config set(%s) failed: %v", c.Key, err)
	}

	return setConfigItem(c)
}

func SetConfigItemUnsafe(c types.Config) error {
	return setConfigItem(c)
}

func GetConfigItem(c types.Config) (types.Configs, error) {
	var err error
	ret := make(types.Configs, 1)
	configTable := GetConstConfigTable()
	who := "mon"

	// safety checks
	canRead, err := canReadConfig(c.Key)
	if !canRead {
		return nil, err
	}

	// workaround to query global configs from mon entity
	// otherwise use the provided entity.
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
	ret[0].Value, err = common.ProcessExec.RunCommand("ceph", args...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func RemoveConfigItem(c types.Config) error {
	// safety checks
	canSet, err := canSetConfig(c.Key)
	if !canSet {
		return err
	}

	args := []string{
		"config",
		"rm",
		GetConstConfigTable()[c.Key].Who,
		c.Key,
	}

	_, err = common.ProcessExec.RunCommand("ceph", args...)
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

	output, err := common.ProcessExec.RunCommand("ceph", args...)
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

// ****** Helper Functions ******//
func setConfigItem(c types.Config) error {
	args := []string{
		"config",
		"set",
		GetConstConfigTable()[c.Key].Who,
		c.Key,
		c.Value,
		"-f",
		"json-pretty",
	}

	_, err := common.ProcessExec.RunCommand("ceph", args...)
	if err != nil {
		return err
	}

	return nil
}

// canSetConfig checks if the config option is configurable.
func canSetConfig(key string) (bool, error) {
	config, err := getClusterConfigDefinition(key)
	if err != nil {
		logger.Warnf(err.Error())
		return false, err
	}

	if config.Permission != ClusterConfigRW {
		err := fmt.Errorf("requested key %s does not support write operation", key)
		logger.Warnf(err.Error())
		return false, err
	}

	return true, nil
}

func canReadConfig(key string) (bool, error) {
	_, err := getClusterConfigDefinition(key)
	if err != nil {
		logger.Warnf(err.Error())
		return false, err
	}

	return true, nil
}

func getClusterConfigDefinition(key string) (ClusterConfigDefinition, error) {
	configTable := GetConstConfigTable()
	config, ok := configTable[key]
	if !ok {
		err := fmt.Errorf("requested key %s is not a MicroCeph supported cluster config", key)
		logger.Warnf(err.Error())
		return config, err
	}

	return config, nil
}

// backwardCompatPubnet ensures that the public_network is set in the database
// this is a backward-compat shim to accomodate older versions of microceph
// which will ensure that the public_network is set in the database
func backwardCompatPubnet(ctx context.Context, s interfaces.StateInterface) error {
	config, err := GetConfigDb(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get config from db: %w", err)
	}

	// do we have a public_network configured?
	// if it is unset, the below will evaluate to the empty string
	// and subsequently fail the net.ParseCIDR check
	pubNet := config["public_network"]
	_, _, err = net.ParseCIDR(pubNet)
	if err != nil {
		// get public network from default address
		pubNet, err = common.Network.FindNetworkAddress(s.ClusterState().Address().Hostname())
		if err != nil {
			return fmt.Errorf("failed to locate public network: %w", err)
		}
		// update the database
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "public_network", Value: pubNet})
			if err != nil {
				return fmt.Errorf("failed to record public_network: %w", err)
			}
			return nil
		})
	}

	return nil
}

// backwardCompatMonitors retrieves monitor addresses from the node list and returns that
// this a backward-compat shim to accomodate older versions of microceph
func backwardCompatMonitors(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	var err error
	var monitors []database.Service
	serviceName := "mon"

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		monitors, err = database.GetServices(ctx, tx, database.ServiceFilter{Service: &serviceName})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
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
	return monitorAddresses, nil
}

// UpdateConfig updates the ceph.conf file with the current configuration.
func UpdateConfig(ctx context.Context, s interfaces.StateInterface) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(filepath.Dir(os.Getenv("SNAP_DATA")), "current", "run")

	err := backwardCompatPubnet(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to ensure backward compat: %w", err)
	}

	config, err := GetConfigDb(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get config db: %w", err)
	}

	// REF: https://docs.ceph.com/en/quincy/rados/configuration/network-config-ref/#ceph-daemons
	// The mon host configuration option only needs to be sufficiently up to date such that a
	// client can reach one monitor that is currently online.
	monitorAddresses := getMonitorsFromConfig(config)

	// backward compat: if no mon hosts found, get them from the node addresses but don't
	// insert into db, as the join logic will take care of that.
	if len(monitorAddresses) == 0 {
		monitorAddresses, err = backwardCompatMonitors(ctx, s)
		if err != nil {
			return fmt.Errorf("failed to get monitor addresses: %w", err)
		}
	}

	// Ensure that IPv6 addresses have square brackets around them (if IPv6 is used).
	monitorAddresses = formatIPv6(monitorAddresses)

	conf := NewCephConfig(constants.CephConfFileName)

	// Check if host has IP address on the configured public network.
	_, err = common.Network.FindIpOnSubnet(config["public_network"])
	if err != nil {
		return fmt.Errorf("failed to locate IP on public network %s: %w", config["public_network"], err)
	}

	clientConfig, err := GetClientConfigForHost(ctx, s, s.ClusterState().Name())
	if err != nil {
		logger.Errorf("Failed to pull Client Configurations: %v", err)
		return err
	}

	// Populate Template
	err = conf.WriteConfig(
		map[string]any{
			"fsid":                config["fsid"],
			"runDir":              runPath,
			"monitors":            strings.Join(monitorAddresses, ","),
			"pubNet":              config["public_network"],
			"ipv4":                strings.Contains(config["public_network"], "."),
			"ipv6":                strings.Contains(config["public_network"], ":"),
			"isCache":             clientConfig.IsCache,
			"cacheSize":           clientConfig.CacheSize,
			"isCacheWritethrough": clientConfig.IsCacheWritethrough,
			"cacheMaxDirty":       clientConfig.CacheMaxDirty,
			"cacheTargetDirty":    clientConfig.CacheTargetDirty,
		},
		0644,
	)
	if err != nil {
		return fmt.Errorf("couldn't render ceph.conf: %w", err)
	}
	logger.Debugf("updated ceph.conf: %v", conf.GetPath())

	// Generate ceph.client.admin.keyring
	keyring := NewCephKeyring(confPath, "ceph.keyring")
	err = keyring.WriteConfig(
		map[string]any{
			"name": "client.admin",
			"key":  config["keyring.client.admin"],
		},
		0640,
	)
	if err != nil {
		return fmt.Errorf("couldn't render ceph.client.admin.keyring: %w", err)
	}

	return nil
}

// GetConfigDb retrieves the configuration from the database.
func GetConfigDb(ctx context.Context, s interfaces.StateInterface) (map[string]string, error) {
	var err error
	var configItems []database.ConfigItem

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		configItems, err = database.GetConfigItems(ctx, tx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	config := map[string]string{}
	for _, item := range configItems {
		config[item.Key] = item.Value
	}
	return config, nil
}

// GetMonitorAddresses retrieves the monitor addresses from the database.
func GetMonitorAddresses(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	config, err := GetConfigDb(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("failed to get config db: %w", err)
	}

	monitorAddresses := getMonitorsFromConfig(config)

	if len(monitorAddresses) == 0 {
		monitorAddresses, err = backwardCompatMonitors(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("failed to get monitor addresses: %w", err)
		}
	}

	// Ensure that IPv6 addresses have square brackets around them (if IPv6 is used).
	monitorAddresses = formatIPv6(monitorAddresses)
	return monitorAddresses, nil
}

// getMonitorsFromConfig scans a provided config key/value map and returns a list of mon hosts found.
func getMonitorsFromConfig(configs map[string]string) []string {
	monHosts := []string{}
	for k, v := range configs {
		if strings.Contains(k, "mon.host.") {
			monHosts = append(monHosts, v)
		}
	}
	return monHosts
}

// formatIPv6 returns a slice in which all IPv6 addresses are formatted with square brackets.
func formatIPv6(addrs []string) []string {
	formatted := []string{}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip != nil && strings.Contains(addr, ":") {
			addr = fmt.Sprintf("[%s]", addr)
		}

		formatted = append(formatted, addr)
	}

	return formatted
}
