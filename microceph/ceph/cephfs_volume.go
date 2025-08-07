package ceph

import (
	"fmt"
	"unsafe"

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
	Subvolume          string
	GroupedSubvolume   string
	UngroupedSubvolume string
	Subvolumes         []Subvolume
)

func ListCephFSVolumes() ([]string, error) {
	args := []string{"fs", "volume", "ls", "--format=json"}
	output, err := cephRun(args...)
	if err != nil {
		return nil, err
	}

	volumes := gjson.Get(output, "#.name")
	response := make([]string, len(volumes.Array()))
	for _, volume := range volumes.Array() {
		response = append(response, volume.String())
	}

	return response, nil
}

func GetCephFSVolume(volume string) (CephFSVolume, error) {
	response := CephFSVolume{Name: volume}
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
	return response, nil
}

func GetCephFSSubvolumeGroups(volume string) (map[string]Subvolumegroup, error) {
	args := []string{"fs", "subvolumegroup", "ls", volume, "--format=json"}
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

func GetCephFSSubvolumes(volume, subvolumegroup string) ([]Subvolume, error) {
	var args []string

	if len(subvolumegroup) == 0 {
		args = []string{"fs", "subvolume", "ls", volume, subvolumegroup, "--format=json"}
	} else {
		args = []string{"fs", "subvolume", "ls", volume, "--format=json"}
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
