package ceph

import "github.com/canonical/microceph/microceph/constants"

func isRbdPoolMirroringEnabled(_ string) bool {
	// TODO: Implement
	return true
}
func isRbdImageMirroringEnabled(_ string) bool {
	// TODO: Implement
	return true
}

// GetRbdMirroringState checks if resource (expressed as  $pool/$image) has mirroring enabled
func GetRbdMirroringState(resource string) string { return constants.StateEnabledReplication }
