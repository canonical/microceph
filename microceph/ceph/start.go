package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type cephVersionElem map[string]int32

type cephVersion struct {
	Mon     cephVersionElem `json:"mon"`
	Mgr     cephVersionElem `json:"mgr"`
	Osd     cephVersionElem `json:"osd"`
	Mds     cephVersionElem `json:"mds"`
	Overall cephVersionElem `json:"overall"`
}

// getCurrentVersion extracts the version codename from the 'ceph -v' output
func getCurrentVersion() (string, error) {
	output, err := processExec.RunCommand("ceph", "-v")
	if err != nil {
		return "", fmt.Errorf("failed to get ceph version: %w", err)
	}

	parts := strings.Fields(output)
	if len(parts) < 6 { // need sth like "ceph version 19.2.0 (e7ad534...) squid (stable)"
		return "", fmt.Errorf("invalid version string format: %s", output)
	}

	return parts[len(parts)-2], nil // second to last is version code name
}

// checkVersions checks if all Ceph services are running the same version
// retry up to 10 times if multiple versions are detected to allow for upgrades to complete as they are performed
// concurrently
func checkVersions() (bool, error) {
	const (
		maxRetries = 10
		retryDelay = 10 * time.Second
	)

	for attempt := 0; attempt < maxRetries; attempt++ {
		out, err := processExec.RunCommand("ceph", "versions")
		if err != nil {
			return false, fmt.Errorf("failed to get Ceph versions: %w", err)
		}

		var cephVer cephVersion
		err = json.Unmarshal([]byte(out), &cephVer)
		if err != nil {
			return false, fmt.Errorf("failed to unmarshal Ceph versions: %w", err)
		}

		if len(cephVer.Overall) > 1 {
			if attempt < maxRetries-1 {
				logger.Debugf("multiple versions detected (attempt %d/%d), waiting %v before retry",
					attempt+1, maxRetries, retryDelay)
				time.Sleep(retryDelay)
				continue
			}
			logger.Debug("not all upgrades have completed after retries")
			return false, nil
		}

		if len(cephVer.Osd) < 1 {
			logger.Debug("no OSD versions found")
			return false, nil
		}

		return true, nil
	}
	// this should never be reached
	return false, nil
}

func osdReleaseRequired(version string) (bool, error) {
	out, err := processExec.RunCommand("ceph", "osd", "dump", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("failed to get OSD dump: %w", err)
	}

	var result map[string]any
	err = json.Unmarshal([]byte(out), &result)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal OSD dump: %w", err)
	}

	releaseVersion, ok := result["require_osd_release"].(string)
	if !ok {
		return false, fmt.Errorf("invalid or missing require_osd_release in OSD dump")
	}

	return releaseVersion != version, nil
}

func updateOSDRelease(version string) error {
	_, err := processExec.RunCommand("ceph", "osd", "require-osd-release",
		version, "--yes-i-really-mean-it")
	if err != nil {
		return fmt.Errorf("failed to update OSD release version: %w", err)
	}
	return nil
}

// PostRefresh handles version checking and OSD release updates
func PostRefresh() error {
	currentVersion, err := getCurrentVersion()
	if err != nil {
		return fmt.Errorf("version check failed: %w", err)
	}

	allVersionsEqual, err := checkVersions()
	if err != nil {
		return fmt.Errorf("version equality check failed: %w", err)
	}

	if !allVersionsEqual {
		logger.Info("versions not equal, skipping OSD release update")
		return nil
	}

	mustUpdate, err := osdReleaseRequired(currentVersion)
	if err != nil {
		return fmt.Errorf("OSD release check failed: %w", err)
	}

	if !mustUpdate {
		logger.Debug("OSD release update not required")
		return nil
	}
	err = updateOSDRelease(currentVersion)
	if err != nil {
		return fmt.Errorf("OSD release update failed: %w", err)
	}

	logger.Infof("successfully updated OSD release version: %s", currentVersion)
	return nil
}

// Start is run on daemon startup.
func Start(ctx context.Context, s interfaces.StateInterface) error {
	// Start background loop to refresh the config every minute if needed.
	go func() {
		oldMonitors := []string{}

		for {
			// Check that the database is ready.
			err := s.ClusterState().Database().IsOpen(context.Background())
			if err != nil {
				logger.Debug("start: database not ready, waiting...")
				time.Sleep(10 * time.Second)
				continue
			}

			// Get the current list of monitors.
			monitors := []string{}
			err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				serviceName := "mon"
				services, err := database.GetServices(ctx, tx, database.ServiceFilter{Service: &serviceName})
				if err != nil {
					return err
				}

				for _, service := range services {
					monitors = append(monitors, service.Member)
				}

				return nil
			})
			if err != nil {
				logger.Warnf("start: failed to fetch monitors, retrying: %v", err)
				time.Sleep(10 * time.Second)
				continue
			}

			// Compare to the previous list.
			if reflect.DeepEqual(oldMonitors, monitors) {
				logger.Debugf("start: monitors unchanged, sleeping: %v", monitors)
				time.Sleep(time.Minute)
				continue
			}

			err = UpdateConfig(ctx, s)
			if err != nil {
				logger.Errorf("start: failed to update config, retrying: %v", err)
				time.Sleep(10 * time.Second)
				continue
			}
			logger.Debug("start: updated config, sleeping")
			oldMonitors = monitors
			time.Sleep(time.Minute)
		}
	}()

	go func() {
		time.Sleep(10 * time.Second) // wait for the mons to converge
		err := PostRefresh()
		if err != nil {
			logger.Errorf("PostRefresh failed: %v", err)
		}
	}()

	return nil
}
