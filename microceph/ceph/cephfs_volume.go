package ceph

import (
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/tidwall/gjson"
)

// CephFSVolume represents a CephFS volume with its subvolume groups and ungrouped subvolumes.
type CephFSVolume struct {
	Name                string
	SubvolumeGroups     map[string]Subvolumegroup
	UngroupedSubVolumes []UngroupedSubvolume
}

// Subvolumegroup represents a group of subvolumes within a CephFS volume.
type Subvolumegroup struct {
	SubVolumes []GroupedSubvolume
}

// Typed Strings for better readability
type (
	Volume             string
	Subvolume          string
	GroupedSubvolume   string
	UngroupedSubvolume string
	Subvolumes         []Subvolume
)

// Example path of a subvolume /volumes/subvolumegroup/subvolume/
const (
	CephFsSubVolumeGroupIndex = 2 // "subvolume group name"
	CephFsSubVolumeIndex      = 3 // "subvolume name"
)

// ListCephFSVolumes lists all CephFS volumes in the cluster.
func ListCephFSVolumes() ([]Volume, error) {
	args := []string{"fs", "volume", "ls", "--format=json"}
	output, err := cephRun(args...)
	if err != nil {
		return nil, err
	}

	logger.Infof("FSVOL: listed %s as cephfs volume", output)

	volumes := gjson.Get(output, "#.name")
	response := make([]Volume, 0, len(volumes.Array()))
	for _, volume := range volumes.Array() {
		if len(volume.String()) == 0 {
			continue
		}

		logger.Infof("FSVOL: found %s as cephfs volume", volume.String())
		response = append(response, Volume(volume.String()))
	}

	return parseListVolOutputJson(output)
}

func ListRemoteCephFSVolumes(remote string, local string) ([]Volume, error) {
	if len(remote) == 0 || len(local) == 0 {
		return nil, fmt.Errorf("both remote(%s) and local(%s) names must be provided", remote, local)
	}

	args := []string{"fs", "volume", "ls", "--format=json"}
	cmd := appendRemoteClusterArgs(args, remote, local)

	output, err := cephRun(cmd...)
	if err != nil {
		return nil, err
	}

	logger.Infof("FSVOL: listed %s as remote cluster (%s) cephfs volumes", output, remote)

	return parseListVolOutputJson(output)
}

// CephFsSubvolumePathDeconstruct deconstructs a CephFS subvolume path string into its subvolume and subvolumegroup names.
func CephFsSubvolumePathDeconstruct(path string) (subvolumegroup string, subvolume string, err error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("invalid CephFS subvolume path: %s", path)
	}

	logger.Debugf("FSVOL: %+v", parts)

	subvolumegroup = parts[CephFsSubVolumeGroupIndex]
	subvolume = parts[CephFsSubVolumeIndex]

	return subvolumegroup, subvolume, nil
}

// GetCephFSVolume returns a CephFSVolume struct representing the specified volume,
// including its subvolume groups and ungrouped subvolumes.
func GetCephFSVolume(volume Volume) (CephFSVolume, error) {
	response := CephFSVolume{Name: string(volume)}
	var err error

	response.SubvolumeGroups, err = GetCephFSSubvolumeGroups(volume)
	if err != nil {
		return response, fmt.Errorf("failed to get subvolume groups for CephFS volume %s: %w", volume, err)
	}

	Subvolumes, err := GetCephFSSubvolumes(volume, "")
	if err != nil {
		return response, fmt.Errorf("failed to get ungrouped subvolumes for CephFS volume %s: %w", volume, err)
	}

	ungroupedSubVolumes := make([]UngroupedSubvolume, 0, len(Subvolumes))
	for _, subvolume := range Subvolumes {
		ungroupedSubVolumes = append(ungroupedSubVolumes, UngroupedSubvolume(subvolume))
	}
	response.UngroupedSubVolumes = ungroupedSubVolumes

	logger.Debugf("VOLCFS: Fetched volumes %s as %v", volume, response)
	return response, nil
}

func CephFSSubvolumeExists(volume string, subvolumegroup string, subvolume string) bool {
	subvolumes, err := GetCephFSSubvolumes(Volume(volume), subvolumegroup)
	if err != nil {
		return false
	}

	for _, sv := range subvolumes {
		if string(sv) == subvolume {
			return true
		}
	}

	return false
}

// GetCephFSSubvolumeGroups lists all subvolume groups in the specified CephFS volume.
func GetCephFSSubvolumeGroups(volume Volume) (map[string]Subvolumegroup, error) {
	args := []string{"fs", "subvolumegroup", "ls", string(volume), "--format=json"}
	output, err := cephRun(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list subvolume groups for CephFS volume %s: %w", volume, err)
	}

	svgNameList := []string{}
	subvolumegroups := gjson.Get(output, "#.name")
	for _, subvolumegroup := range subvolumegroups.Array() {
		svgNameList = append(svgNameList, subvolumegroup.String())
	}

	response := map[string]Subvolumegroup{}
	for _, svgName := range svgNameList {
		subvolumes, err := GetCephFSSubvolumes(volume, svgName)
		if err != nil {
			return nil, err
		}

		groupedSubvolume := make([]GroupedSubvolume, 0, len(subvolumes))
		for _, subvolume := range subvolumes {
			groupedSubvolume = append(groupedSubvolume, GroupedSubvolume(subvolume))
		}

		response[svgName] = Subvolumegroup{SubVolumes: groupedSubvolume}
	}

	return response, nil
}

// GetCephFSSubvolumes lists all subvolumes in the specified CephFS volume and optionally the subvolume group.
func GetCephFSSubvolumes(volume Volume, subvolumegroup string) ([]Subvolume, error) {
	var args []string

	if len(subvolumegroup) != 0 {
		args = []string{"fs", "subvolume", "ls", string(volume), subvolumegroup, "--format=json"}
	} else {
		args = []string{"fs", "subvolume", "ls", string(volume), "--format=json"}
	}

	output, err := cephRun(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list subvolumes for CephFS volume %s in group %s: %w", volume, subvolumegroup, err)
	}

	subvolumes := gjson.Get(output, "#.name")
	response := make([]Subvolume, 0, len(subvolumes.Array()))
	for _, subvolume := range subvolumes.Array() {
		response = append(response, Subvolume(subvolume.String()))
	}

	return response, nil
}

// GetCephFSSubvolumePath retrieves the full path of a specified subvolume within a CephFS volume and subvolume group.
func GetCephFSSubvolumePath(subvolumegroup string, subvolume string) string {
	var retval string
	if len(subvolumegroup) != 0 {
		retval = fmt.Sprintf("/volumes/%s/%s/", subvolumegroup, subvolume)
	} else {
		retval = fmt.Sprintf("/volumes/_nogroup/%s/", subvolume)
	}

	return retval
}

// ##### Helper Functions #####

func parseListVolOutputJson(output string) ([]Volume, error) {
	volumes := gjson.Get(output, "#.name")
	response := make([]Volume, 0, len(volumes.Array()))
	for _, volume := range volumes.Array() {
		if len(volume.String()) == 0 {
			continue
		}

		logger.Debugf("FSVOL: found %s as cephfs volume", volume.String())
		response = append(response, Volume(volume.String()))
	}

	return response, nil
}
