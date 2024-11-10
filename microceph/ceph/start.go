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

func checkVersions() (bool, error) {
	out, err := processExec.RunCommand("ceph", "versions")
	if err != nil {
		return false, fmt.Errorf("Failed to get Ceph versions: %w", err)
	}

	var cephVer cephVersion
	err = json.Unmarshal([]byte(out), &cephVer)
	if err != nil {
		return false, fmt.Errorf("Failed to unmarshal Ceph versions: %w", err)
	}

	if len(cephVer.Overall) > 1 {
		logger.Debug("Not all upgrades have completed")
		return false, nil
	}

	if len(cephVer.Osd) < 1 {
		logger.Debug("No OSD versions found")
		return false, nil
	}

	return true, nil
}

func osdReleaseRequired(version string) (bool, error) {
	out, err := processExec.RunCommand("ceph", "osd", "dump", "-f", "json")
	if err != nil {
		return false, fmt.Errorf("Failed to get OSD dump: %w", err)
	}

	var result map[string]any
	err = json.Unmarshal([]byte(out), &result)
	if err != nil {
		return false, fmt.Errorf("Failed to unmarshal OSD dump: %w", err)
	}

	return result["require_osd_release"].(string) != version, nil
}

func PostRefresh() error {
	currentVersion, err := processExec.RunCommand("ceph", "-v")
	if err != nil {
		return err
	}

	lastPos := strings.LastIndex(currentVersion, " ")
	if lastPos < 0 {
		return fmt.Errorf("invalid version string: %s", currentVersion)
	}

	currentVersion = currentVersion[0:lastPos]
	lastPos = strings.LastIndex(currentVersion, " ")
	if lastPos < 0 {
		return fmt.Errorf("invalid version string: %s", currentVersion)
	}

	currentVersion = currentVersion[lastPos+1 : len(currentVersion)]
	allVersionsEqual, err := checkVersions()

	if err != nil {
		return err
	}

	if !allVersionsEqual {
		return nil
	}

	mustUpdate, err := osdReleaseRequired(currentVersion)
	if err != nil {
		return err
	}

	if mustUpdate {
		_, err = processExec.RunCommand("ceph", "osd", "require-osd-release",
			currentVersion, "--yes-i-really-mean-it")
		if err != nil {
			return err
		}
	}

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

	go PostRefresh()

	return nil
}
