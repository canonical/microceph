package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// ### Bootstrap helpers contain non public helper methods used in public methods ###

// enableMsgr2 enables msgr v2 addressing for mon
func enableMsgr2() error {
	// Enable msgr2.
	_, err := cephRun("mon", "enable-msgr2")
	if err != nil {
		return fmt.Errorf("Failed to enable msgr2: %w", err)
	}
	return nil
}

// waitForMonitor polls 'ceph mon stat' until it succeeds or the timeout is reached.
func waitForMonitor(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := cephRun("mon", "stat")
		if err == nil {
			logger.Debug("Monitor quorum is active.")
			return nil // Success
		}
		lastErr = err
		logger.Debugf("Waiting for monitor to respond, retrying...")
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for monitor after %v: %w", timeout, lastErr)
}

// ### Bootstrap Daemon Initializers

// initOSDs starts the OSD daemon on host
func initOSDs(_ interfaces.StateInterface, _ string) error {
	// Start OSD service.
	err := snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start OSD service: %w", err)
	}
	return nil
}

// initMon starts the mon service on host
func initMon(s interfaces.StateInterface, dataPath string, path string) error {
	// Bootstrap the initial monitor.
	monDataPath := filepath.Join(dataPath, "mon", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(monDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = bootstrapMon(s.ClusterState().Name(), monDataPath, filepath.Join(path, "mon.map"), filepath.Join(path, "mon.keyring"))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = snapStart("mon", true)
	if err != nil {
		return fmt.Errorf("Failed to start monitor: %w", err)
	}

	logger.Debug("Waiting for monitor to become responsive after starting service...")
	err = waitForMonitor(3 * time.Minute)
	if err != nil {
		// Fail bootstrap if the monitor doesn't become responsive.
		return fmt.Errorf("monitor did not become responsive within timeout: %w", err)
	}
	logger.Debug("Monitor is responsive.")

	return nil
}

// initMgr starts the manager service on host
func initMgr(s interfaces.StateInterface, dataPath string) error {
	// Bootstrap the initial manager.
	mgrDataPath := filepath.Join(dataPath, "mgr", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(mgrDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = bootstrapMgr(s.ClusterState().Name(), mgrDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = snapStart("mgr", true)
	if err != nil {
		return fmt.Errorf("Failed to start manager: %w", err)
	}
	return nil
}

// initMds starts the metadata service on host
func initMds(s interfaces.StateInterface, dataPath string) error {
	// Bootstrap the initial metadata server.
	mdsDataPath := filepath.Join(dataPath, "mds", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(mdsDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = bootstrapMds(s.ClusterState().Name(), mdsDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = snapStart("mds", true)
	if err != nil {
		return fmt.Errorf("Failed to start metadata server: %w", err)
	}
	return nil
}

// ### DB ops to be used in a transaction ###

// bootstrapDBAddServiceOp adds a service to the database within a transaction.
func bootstrapDBAddServiceOp(ctx context.Context, tx *sql.Tx, member string, service string) error {
	_, err := database.CreateService(ctx, tx, database.Service{Member: member, Service: service})
	if err != nil {
		err = fmt.Errorf("failed to record service: Member(%s), Service(%s): %w", member, service, err)
		logger.Error(err.Error())
		return err
	}

	return nil
}

// bootstrapDBAddConfigItemOp adds a config item to the database within a transaction.
func bootstrapDBAddConfigItemOp(ctx context.Context, tx *sql.Tx, Key string, Value string) error {
	_, err := database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: Key, Value: Value})
	if err != nil {
		err = fmt.Errorf("failed to record config item: Key(%s), Value(%s): %w", Key, Value, err)
		logger.Error(err.Error())
		return err
	}

	return nil
}
