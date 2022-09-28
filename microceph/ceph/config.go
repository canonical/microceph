package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/database"
)

func updateConfig(s *state.State) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(os.Getenv("SNAP_DATA"), "run")

	// Get the configuration and servers.
	var err error
	var configItems []database.ConfigItem
	var monitors []database.Service

	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
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
	remotes := s.Remotes().RemotesByName()
	for _, monitor := range monitors {
		remote, ok := remotes[monitor.Member]
		if !ok {
			continue
		}

		monitorAddresses = append(monitorAddresses, remote.Address.Addr().String())
	}

	// Generate ceph.conf.
	fd, err := os.OpenFile(filepath.Join(confPath, "ceph.conf"), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("Couldn't write ceph.conf: %w", err)
	}
	defer fd.Close()

	err = cephConfTpl.Execute(fd, map[string]any{
		"fsid":     config["fsid"],
		"runDir":   runPath,
		"monitors": strings.Join(monitorAddresses, ","),
		"addr":     s.Address().Hostname(),
	})
	if err != nil {
		return fmt.Errorf("Couldn't render ceph.conf: %w", err)
	}

	// Generate ceph.client.admin.keyring
	fd, err = os.OpenFile(filepath.Join(confPath, "ceph.client.admin.keyring"), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("Couldn't write ceph.conf: %w", err)
	}
	defer fd.Close()

	err = cephKeyringTpl.Execute(fd, map[string]any{
		"name": "client.admin",
		"key":  config["keyring.client.admin"],
	})
	if err != nil {
		return fmt.Errorf("Couldn't render ceph.client.admin.keyring: %w", err)
	}

	return nil
}