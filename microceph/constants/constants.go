// Package common
package constants

import (
	"os"
	"path/filepath"
)

// Constants for Size Constraints
const MinOSDSize = uint64(2147483648) // 2GB i.e. 2*1024*1024*1024

const ClientConfigGlobalHostConst = "*"
const BootstrapPortConst = 7443

// Time constants
const RgwRestartAgeThreshold = 2 // seconds

// string templates
const LoopSpecId = "loop,"
const DevicePathPrefix = "/dev/disk/by-id/"
const RgwSockPattern = "client.radosgw.gateway"
const CliForcePrompt = "If you understand the *RISK* and you're *ABSOLUTELY CERTAIN* that is what you want, pass --yes-i-really-mean-it."
const ExperimentalConfigErrTemplate = "WARNING: An operation (%s) was performed on experimental config (%s)"

// Path and filename constants

const CephConfFileName = "ceph.conf"

// Misc
const AdminKeyringFieldName = "keyring.client.admin"
const AdminKeyringTemplate = "keyring.client.%s"

// Ceph Error Substrings
const RbdMirrorNonPrimaryPromoteErr = "image is primary within a remote cluster or demotion is not propagated yet"

type PathConst struct {
	ConfPath     string
	RunPath      string
	DataPath     string
	LogPath      string
	RootFs       string
	ProcPath     string
	SSLFilesPath string
	SnapPath     string
}

type PathFileMode map[string]os.FileMode

var GetPathConst = func() PathConst {
	return PathConst{
		ConfPath:     filepath.Join(os.Getenv("SNAP_DATA"), "conf"),
		RunPath:      filepath.Join(os.Getenv("SNAP_DATA"), "run"),
		DataPath:     filepath.Join(os.Getenv("SNAP_COMMON"), "data"),
		LogPath:      filepath.Join(os.Getenv("SNAP_COMMON"), "logs"),
		RootFs:       filepath.Join(os.Getenv("TEST_ROOT_PATH"), "/"),
		ProcPath:     filepath.Join(os.Getenv("TEST_ROOT_PATH"), "/proc"),
		SSLFilesPath: filepath.Join(os.Getenv("SNAP_COMMON"), "/"),
		SnapPath:     filepath.Join(os.Getenv("SNAP"), "/"),
	}
}

// File Modes
const PermissionWorldNoAccess = 0750
const PermissionOnlyUserAccess = 0700

func GetPathFileMode() PathFileMode {
	pathConsts := GetPathConst()
	return PathFileMode{
		pathConsts.ConfPath: PermissionWorldNoAccess,
		pathConsts.RunPath:  PermissionOnlyUserAccess,
		pathConsts.DataPath: PermissionOnlyUserAccess,
		pathConsts.LogPath:  PermissionOnlyUserAccess,
	}
}

// Regexes
const ClusterNameRegex = "^[a-z0-9]+$"

// Replication Events
const EventEnableReplication = "enable_replication"
const EventDisableReplication = "disable_replication"
const EventListReplication = "list_replication"
const EventStatusReplication = "status_replication"
const EventConfigureReplication = "configure_replication"

// Rbd features
var RbdJournalingEnableFeatureSet = [...]string{"exclusive-lock", "journaling"}

const EventPromoteReplication = "promote_replication"
const EventDemoteReplication = "demote_replication"
