package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

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
	err = conf.WriteConfig(
		map[string]any{
			"fsid":     config["fsid"],
			"runDir":   runPath,
			"monitors": strings.Join(monitorAddresses, ","),
			"addr":     s.ClusterState().Address().Hostname(),
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
