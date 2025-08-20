package ceph

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/canonical/microceph/microceph/logger"
	"github.com/tidwall/gjson"
)

type CephFSVolume struct {
	Name                string
	SubvolumeGroups     map[string]Subvolumegroup
	UngroupedSubVolumes []UngroupedSubvolume
}

type Subvolumegroup struct {
	SubVolumes []GroupedSubvolume
}

type (
	Volume             string
	Subvolume          string
	GroupedSubvolume   string
	UngroupedSubvolume string
	Subvolumes         []Subvolume
)

// Example path of a subvolume /volumes/subvolumegroup/subvolume/
const (
	CephFsConstrantEmptyIndex         = 0 // ""
	CephFsConstrantStringVolumesIndex = 1 // "volumes"
	CephFsSubVolumeGroupIndex         = 2 // "subvolume group name"
	CephFsSubVolumeIndex              = 3 // "subvolume name"
)

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

	return response, nil
}

func CephFsSubvolumePathDeconstruct(path string) (subvolumegroup string, subvolume string, err error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("invalid CephFS subvolume path: %s", path)
	}

	subvolumegroup = parts[CephFsSubVolumeGroupIndex]
	subvolume = parts[CephFsSubVolumeIndex]

	return subvolumegroup, subvolume, nil
}

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

	response.UngroupedSubVolumes = unsafe.Slice(
		(*UngroupedSubvolume)(unsafe.SliceData(Subvolumes)),
		len(Subvolumes),
	)

	logger.Debugf("VOLCFS: Fetched volumes %s as %v", volume, response)
	return response, nil
}

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

		response[svgName] = Subvolumegroup{SubVolumes: unsafe.Slice((*GroupedSubvolume)(unsafe.SliceData(subvolumes)), len(subvolumes))}
	}

	return response, nil
}

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

func GetCephFSSubvolumePath(volume string, subvolumegroup string, subvolume string) (string, error) {
	var args []string

	if len(subvolumegroup) == 0 {
		args = []string{"fs", "subvolume", "getpath", volume, subvolume, subvolumegroup}
	} else {
		args = []string{"fs", "subvolume", "getpath", volume, subvolume}
	}

	return cephRun(args...)
}
