package ceph

func GetCephFsVolumeId(volume string) string {
	// CephFS volume ID is the same as the volume name.
	return volume
}
