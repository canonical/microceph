package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// serviceStartMu is held by bootstrap/join while they start services, and by
// reEnableServices when it re-enables services after a snap disable/enable
// cycle. This prevents concurrent snapctl service-control calls.
var serviceStartMu sync.Mutex

type cephVersionElem map[string]int32

type cephVersion struct {
	Mon     cephVersionElem `json:"mon"`
	Mgr     cephVersionElem `json:"mgr"`
	Osd     cephVersionElem `json:"osd"`
	Mds     cephVersionElem `json:"mds"`
	Overall cephVersionElem `json:"overall"`
}

// versionRetrySleep is a function that can be mocked in tests to control the sleep duration
var versionRetrySleep = time.Sleep

// getCurrentVersion extracts the version codename from the 'ceph -v' output
func getCurrentVersion() (string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "-v")
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
		out, err := common.ProcessExec.RunCommand("ceph", "versions")
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
				versionRetrySleep(retryDelay)
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
	out, err := common.ProcessExec.RunCommand("ceph", "osd", "dump", "-f", "json")
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
	_, err := common.ProcessExec.RunCommand("ceph", "osd", "require-osd-release",
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

// migrateStaleRunDir rewrites radosgw.conf and ganesha.conf when they contain
// a revision-specific run directory path.  When a service is first enabled,
// the run dir is baked into the config file as $SNAP_DATA/run (e.g.
// /var/snap/microceph/1630/run).  After a snap refresh snapd may garbage-
// collect old revision directories, leaving the path dangling.  This function
// replaces any such stale path with the stable 'current' symlink so that
// services can be re-enabled successfully.
func migrateStaleRunDir() {
	pathConsts := constants.GetPathConst()
	err := fixRadosGWRunDir(filepath.Join(pathConsts.ConfPath, "radosgw.conf"), pathConsts.RunPath)
	if err != nil {
		logger.Warnf("migration: failed to update run dir in radosgw.conf: %v", err)
	}
	err = fixGaneshaRunDir(filepath.Join(pathConsts.ConfPath, "ganesha", "ganesha.conf"), pathConsts.RunPath)
	if err != nil {
		logger.Warnf("migration: failed to update run dir in ganesha.conf: %v", err)
	}
}

// fixRadosGWRunDir updates the 'run dir' line in radosgw.conf to correctRunDir.
func fixRadosGWRunDir(confFile, correctRunDir string) error {
	return fixConfigLine(confFile, func(line string) (string, bool) {
		if strings.HasPrefix(strings.TrimSpace(line), "run dir = ") {
			correct := "run dir = " + correctRunDir
			if line == correct {
				return line, false
			}
			return correct, true
		}
		return line, false
	})
}

// fixGaneshaRunDir updates the CCacheDir line in ganesha.conf to use correctRunDir.
func fixGaneshaRunDir(confFile, correctRunDir string) error {
	return fixConfigLine(confFile, func(line string) (string, bool) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `CCacheDir = "`) && strings.HasSuffix(trimmed, `/ganesha";`) {
			trimLeft := strings.TrimLeft(line, "\t ")
			leading := line[:len(line)-len(trimLeft)]
			correct := leading + `CCacheDir = "` + correctRunDir + `/ganesha";`
			if line == correct {
				return line, false
			}
			return correct, true
		}
		return line, false
	})
}

// fixConfigLine reads confFile, applies fn to every line, and atomically
// rewrites the file if any line changed.  Returns nil if the file does not exist.
func fixConfigLine(confFile string, fn func(string) (string, bool)) error {
	data, err := os.ReadFile(confFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		newLine, updated := fn(line)
		if updated {
			lines[i] = newLine
			changed = true
		}
	}

	if !changed {
		return nil
	}

	tmpFile := confFile + ".tmp"
	err = os.WriteFile(tmpFile, []byte(strings.Join(lines, "\n")), constants.PermissionUserRwWorldRAccess)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", tmpFile, err)
	}
	err = os.Rename(tmpFile, confFile)
	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to replace %s: %w", confFile, err)
	}
	logger.Infof("migration: fixed stale run dir in %s", confFile)
	return nil
}

// reEnableServices checks which services are registered in the database for
// the current host and re-enables any that are not currently active. This
// handles the case where "snap disable/enable microceph" leaves the secondary
// services (mon, mgr, mds, osd, rgw, rbd-mirror, etc.) disabled.
// serviceStartMu lock ensures this does not race with bootstrap/join
// which also start services.
func reEnableServices(ctx context.Context, s interfaces.StateInterface) {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()

	hostname := s.ClusterState().Name()

	services, err := getServicesForHost(ctx, s, hostname)
	if err != nil {
		logger.Warnf("start: failed to query services for re-enablement: %v", err)
		return
	}

	// If no services are registered, the node hasn't been bootstrapped or
	// joined yet — nothing to re-enable.
	if len(services) == 0 {
		logger.Debug("start: no services registered, skipping re-enablement")
		return
	}

	for _, service := range services {
		if err := snapCheckActive(service.Service); err != nil {
			logger.Infof("start: re-enabling inactive service %q", service.Service)
			if err := snapStart(service.Service, true); err != nil {
				logger.Warnf("start: failed to re-enable service %q: %v", service.Service, err)
			}
		}
	}

	// OSD is not tracked in the services table but is always enabled at
	// bootstrap, so re-enable it if inactive.
	if err := snapCheckActive("osd"); err != nil {
		logger.Infof("start: re-enabling inactive OSD service")
		if err := snapStart("osd", true); err != nil {
			logger.Warnf("start: failed to re-enable OSD service: %v", err)
		}
	}

	// Grouped services (e.g. NFS) are tracked in a separate table.
	groupedServices, err := database.GroupedServicesQuery.GetGroupedServicesOnHost(ctx, s)
	if err != nil {
		logger.Warnf("start: failed to query grouped services for re-enablement: %v", err)
		return
	}

	// De-duplicate by service name since there may be multiple groups.
	seen := map[string]bool{}
	for _, gs := range groupedServices {
		if seen[gs.Service] {
			continue
		}
		seen[gs.Service] = true
		if err := snapCheckActive(gs.Service); err != nil {
			logger.Infof("start: re-enabling inactive grouped service %q", gs.Service)
			if err := snapStart(gs.Service, true); err != nil {
				logger.Warnf("start: failed to re-enable grouped service %q: %v", gs.Service, err)
			}
		}
	}
}

// shouldSkipMonitorRefresh returns true if the background loop should skip
// calling UpdateConfig this iteration. It skips when it is not the first run
// and the monitor list is unchanged.
func shouldSkipMonitorRefresh(first bool, oldMonitors, monitors []string) bool {
	return !first && reflect.DeepEqual(oldMonitors, monitors)
}

// Start is run on daemon startup.
func Start(ctx context.Context, s interfaces.StateInterface) error {
	// flag: are we on the first run?
	first := true
	// Start background loop to refresh the config every minute if needed.
	go func() {
		oldMonitors := []string{}

		for {
			// Check that the database is ready.
			err := s.ClusterState().Database().IsOpen(ctx)
			if err != nil {
				logger.Debug("start: database not ready, waiting...")
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
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
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
				continue
			}

			// Check if we need to update
			if shouldSkipMonitorRefresh(first, oldMonitors, monitors) {
				logger.Debugf("start: monitors unchanged, sleeping: %v", monitors)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Minute):
				}
				continue
			}

			err = UpdateConfig(ctx, s)
			if err != nil {
				logger.Errorf("start: failed to update config, retrying: %v", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
				continue
			}
			logger.Debug("start: updated config, sleeping")
			first = false // for subsequent runs
			oldMonitors = monitors
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()

	// Re-enable services that should be running on this host but may have
	// been left disabled after a snap disable/enable cycle.
	go func() {
		// Wait for the database to become ready.
		for {
			err := s.ClusterState().Database().IsOpen(ctx)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		migrateStaleRunDir()
		reEnableServices(ctx, s)
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second): // wait for the mons to converge
		}
		err := PostRefresh()
		if err != nil {
			logger.Errorf("PostRefresh failed: %v", err)
		}
	}()

	return nil
}
