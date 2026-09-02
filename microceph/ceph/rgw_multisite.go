package ceph

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
)

// ErrRgwSyncStatusUnreadable marks a sync status read whose radosgw-admin
// command failed outright. Callers use it to tell "the command could not
// run at all" apart from a malformed or self-contradictory response, which
// keeps propagating as an ordinary error.
var ErrRgwSyncStatusUnreadable = errors.New("rgw sync status could not be read")

// radosgwAdminRun runs radosgw-admin with the given arguments.
func radosgwAdminRun(args ...string) (string, error) {
	return common.ProcessExec.RunCommand("radosgw-admin", args...)
}

// radosgwAdminRunRemote runs radosgw-admin against an imported remote
// cluster when the cluster/client pair is non-empty, and locally otherwise.
func radosgwAdminRunRemote(cluster string, client string, args ...string) (string, error) {
	args = appendRemoteClusterArgs(args, cluster, client)
	return radosgwAdminRun(args...)
}

// RgwRealm is the subset of `realm get` output RGW replication uses.
type RgwRealm struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CurrentPeriod string `json:"current_period"`
	Epoch         int    `json:"epoch"`
}

// RgwZoneGroupZone is one zone entry in a zonegroup map.
type RgwZoneGroupZone struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Endpoints []string `json:"endpoints"`
	ReadOnly  bool     `json:"read_only"`
}

// RgwZoneGroup is the subset of `zonegroup get` output we use.
type RgwZoneGroup struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	IsMaster   bool               `json:"is_master"`
	Endpoints  []string           `json:"endpoints"`
	MasterZone string             `json:"master_zone"`
	Zones      []RgwZoneGroupZone `json:"zones"`
	RealmID    string             `json:"realm_id"`
}

// RgwZoneSystemKey is the S3 key pair bound to a zone for inter-zone sync.
type RgwZoneSystemKey struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// RgwZone is the subset of `zone get` output we use.
type RgwZone struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	SystemKey RgwZoneSystemKey `json:"system_key"`
}

// GetRgwRealm fetches the default realm. A non-empty cluster/client pair
// targets a remote cluster. A failing command yields a zero value and a
// nil error, so an unconfigured gateway reads as ordinary empty state.
func GetRgwRealm(cluster string, client string) (RgwRealm, error) {
	response := RgwRealm{}

	output, err := radosgwAdminRunRemote(cluster, client, "realm", "get")
	if err != nil {
		logger.Warnf("REPRGW: failed realm get operation: %v", err)
		return response, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return response, fmt.Errorf("cannot unmarshal realm get output: %w", err)
	}

	return response, nil
}

// GetRgwZoneGroup fetches the default zonegroup. A non-empty
// cluster/client pair targets a remote cluster. A failing command yields
// a zero value and a nil error.
func GetRgwZoneGroup(cluster string, client string) (RgwZoneGroup, error) {
	response := RgwZoneGroup{}

	output, err := radosgwAdminRunRemote(cluster, client, "zonegroup", "get")
	if err != nil {
		logger.Warnf("REPRGW: failed zonegroup get operation: %v", err)
		return response, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return response, fmt.Errorf("cannot unmarshal zonegroup get output: %w", err)
	}

	return response, nil
}

// GetRgwZone fetches the default zone. A non-empty cluster/client pair
// targets a remote cluster. A failing command yields a zero value and a
// nil error.
func GetRgwZone(cluster string, client string) (RgwZone, error) {
	response := RgwZone{}

	output, err := radosgwAdminRunRemote(cluster, client, "zone", "get")
	if err != nil {
		logger.Warnf("REPRGW: failed zone get operation: %v", err)
		return response, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return response, fmt.Errorf("cannot unmarshal zone get output: %w", err)
	}

	return response, nil
}

// RgwPeriodMap is the subset of the period's zonegroup directory RGW
// replication uses: every zonegroup in the realm, each with its own zone
// list, not just the one the local zone belongs to.
type RgwPeriodMap struct {
	ZoneGroups []RgwZoneGroup `json:"zonegroups"`
}

// RgwPeriod is the subset of `period get` output RGW replication uses.
// MasterZone is the realm's metadata master - the master zone of the
// realm's master zonegroup - which differs from the local zonegroup's own
// master_zone whenever the local zonegroup is not the realm's master.
type RgwPeriod struct {
	MasterZonegroup string       `json:"master_zonegroup"`
	MasterZone      string       `json:"master_zone"`
	PeriodMap       RgwPeriodMap `json:"period_map"`
}

// GetRgwPeriod fetches the realm's current period, the one topology read
// that describes every zonegroup in the realm rather than only the local
// one. A non-empty cluster/client pair targets a remote cluster. A failing
// command yields a zero value and a nil error, so an unconfigured gateway
// reads as ordinary empty state.
func GetRgwPeriod(cluster string, client string) (RgwPeriod, error) {
	response := RgwPeriod{}

	output, err := radosgwAdminRunRemote(cluster, client, "period", "get")
	if err != nil {
		logger.Warnf("REPRGW: failed period get operation: %v", err)
		return response, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return response, fmt.Errorf("cannot unmarshal period get output: %w", err)
	}

	return response, nil
}

// RgwSyncInfo is the info block shared by `metadata sync status` and
// `data sync status` output. Period and RealmEpoch are metadata-only.
type RgwSyncInfo struct {
	Status     string `json:"status"`
	NumShards  int    `json:"num_shards"`
	Period     string `json:"period"`
	RealmEpoch int    `json:"realm_epoch"`
}

// rgwSyncStateSync is the Status value meaning the zone is actively
// syncing. The other two radosgw-admin reports are "init" and
// "building-full-sync-maps".
const rgwSyncStateSync = "sync"

// RgwMetadataSyncState is one shard's metadata sync state. radosgw-admin
// reports it as a plain number.
type RgwMetadataSyncState int

const (
	RgwMetadataSyncStateFullSync RgwMetadataSyncState = iota
	RgwMetadataSyncStateIncremental
)

// RgwMetadataSyncMarker is one shard's metadata sync position.
type RgwMetadataSyncMarker struct {
	State  RgwMetadataSyncState `json:"state"`
	Marker string               `json:"marker"`
}

// RgwMetadataSyncShard pairs a shard id with its metadata sync marker.
type RgwMetadataSyncShard struct {
	Key int                   `json:"key"`
	Val RgwMetadataSyncMarker `json:"val"`
}

// RgwMetadataSyncStatus is the parsed form of `metadata sync status`. A
// metadata master reports "init" with zero shards - it syncs from no one.
type RgwMetadataSyncStatus struct {
	Info    RgwSyncInfo
	Markers []RgwMetadataSyncShard
}

// RgwDataSyncMarker is one shard's data sync position. Unlike the metadata
// variant, state is a string here: "full-sync" or "incremental-sync".
type RgwDataSyncMarker struct {
	Status string `json:"status"`
	Marker string `json:"marker"`
}

// RgwDataSyncShard pairs a shard id with its data sync marker.
type RgwDataSyncShard struct {
	Key int               `json:"key"`
	Val RgwDataSyncMarker `json:"val"`
}

// RgwDataSyncStatus is the parsed form of `data sync status` for one
// source zone.
type RgwDataSyncStatus struct {
	Info    RgwSyncInfo
	Markers []RgwDataSyncShard
}

type rgwMetadataSyncEnvelope struct {
	SyncStatus struct {
		Info    RgwSyncInfo            `json:"info"`
		Markers []RgwMetadataSyncShard `json:"markers"`
	} `json:"sync_status"`
}

type rgwDataSyncEnvelope struct {
	SyncStatus struct {
		Info    RgwSyncInfo        `json:"info"`
		Markers []RgwDataSyncShard `json:"markers"`
	} `json:"sync_status"`
}

// validateRgwSyncShards rejects a sync status response that contradicts
// itself, since callers index a peer's log by these shard keys.
func validateRgwSyncShards(numShards int, keys []int) error {
	if numShards < 0 {
		return fmt.Errorf("num_shards is negative: %d", numShards)
	}
	if numShards == 0 && len(keys) > 0 {
		return fmt.Errorf("num_shards is 0 but %d marker(s) were reported", len(keys))
	}
	for _, key := range keys {
		if key < 0 || key >= numShards {
			return fmt.Errorf("marker key %d is out of range for num_shards %d", key, numShards)
		}
	}
	return nil
}

// validateRgwMetadataSyncShards checks the metadata markers' shard keys.
func validateRgwMetadataSyncShards(numShards int, markers []RgwMetadataSyncShard) error {
	keys := make([]int, len(markers))
	for i, marker := range markers {
		keys[i] = marker.Key
	}
	return validateRgwSyncShards(numShards, keys)
}

// validateRgwDataSyncShards checks the data markers' shard keys.
func validateRgwDataSyncShards(numShards int, markers []RgwDataSyncShard) error {
	keys := make([]int, len(markers))
	for i, marker := range markers {
		keys[i] = marker.Key
	}
	return validateRgwSyncShards(numShards, keys)
}

// GetRgwMetadataSyncStatus fetches this zone's own metadata sync markers -
// local progress only, no peer contact. A non-empty cluster/client pair
// targets a remote cluster. A failing command returns an error wrapping
// ErrRgwSyncStatusUnreadable rather than a zero value: a zero value would
// later compare as behind, which is a claim this read never made.
func GetRgwMetadataSyncStatus(cluster string, client string) (RgwMetadataSyncStatus, error) {
	envelope := rgwMetadataSyncEnvelope{}

	output, err := radosgwAdminRunRemote(cluster, client, "metadata", "sync", "status")
	if err != nil {
		return RgwMetadataSyncStatus{}, fmt.Errorf("%w: failed metadata sync status operation: %w", ErrRgwSyncStatusUnreadable, err)
	}

	err = json.Unmarshal([]byte(output), &envelope)
	if err != nil {
		return RgwMetadataSyncStatus{}, fmt.Errorf("cannot unmarshal metadata sync status output: %w", err)
	}

	err = validateRgwMetadataSyncShards(envelope.SyncStatus.Info.NumShards, envelope.SyncStatus.Markers)
	if err != nil {
		return RgwMetadataSyncStatus{}, fmt.Errorf("metadata sync status response failed validation: %w", err)
	}

	return RgwMetadataSyncStatus{Info: envelope.SyncStatus.Info, Markers: envelope.SyncStatus.Markers}, nil
}

// GetRgwDataSyncStatus fetches this zone's own data sync markers for one
// source zone - local progress only, no contact with the source. A
// non-empty cluster/client pair targets a remote cluster. A failing
// command returns an error wrapping ErrRgwSyncStatusUnreadable rather than
// a zero value, for the same reason as GetRgwMetadataSyncStatus.
func GetRgwDataSyncStatus(sourceZone string, cluster string, client string) (RgwDataSyncStatus, error) {
	envelope := rgwDataSyncEnvelope{}

	output, err := radosgwAdminRunRemote(cluster, client, "data", "sync", "status", "--source-zone", sourceZone)
	if err != nil {
		return RgwDataSyncStatus{}, fmt.Errorf("%w: failed data sync status operation for source %q: %w", ErrRgwSyncStatusUnreadable, sourceZone, err)
	}

	err = json.Unmarshal([]byte(output), &envelope)
	if err != nil {
		return RgwDataSyncStatus{}, fmt.Errorf("cannot unmarshal data sync status output: %w", err)
	}

	err = validateRgwDataSyncShards(envelope.SyncStatus.Info.NumShards, envelope.SyncStatus.Markers)
	if err != nil {
		return RgwDataSyncStatus{}, fmt.Errorf("data sync status response failed validation: %w", err)
	}

	return RgwDataSyncStatus{Info: envelope.SyncStatus.Info, Markers: envelope.SyncStatus.Markers}, nil
}

// RgwLogShard is one shard of `mdlog status` or `datalog status` output:
// the log head on the cluster that owns it. Array index is the shard id.
//
// Marker is compared against a local sync marker with a plain string
// comparison. That is deliberate, not an oversight: radosgw-admin does
// exactly the same (get_md_sync_status and get_data_sync_status in
// src/rgw/rgw_admin.cc), and every RGW marker format is zero padded to a
// fixed width, so byte order is time order. cls_log markers are "1_"
// followed by a 10 digit second and a 6 digit microsecond field; FIFO
// markers are two 20 digit fields; a datalog generation above 0 prefixes
// "G" and a 20 digit id, which sorts above any digit leading generation 0
// marker. Do not "fix" this into a numeric or timestamp comparison - it
// would break the ordering these formats exist to provide.
type RgwLogShard struct {
	Marker     string `json:"marker"`
	LastUpdate string `json:"last_update"`
}

// GetRgwMdlogStatus fetches the metadata log head for every shard. Point
// it at the metadata master to check a secondary's progress against it.
// A failing command returns nil; a successful one always returns a slice,
// empty log or not.
func GetRgwMdlogStatus(cluster string, client string) ([]RgwLogShard, error) {
	shards := []RgwLogShard{}

	output, err := radosgwAdminRunRemote(cluster, client, "mdlog", "status")
	if err != nil {
		logger.Warnf("REPRGW: failed mdlog status operation: %v", err)
		return nil, nil
	}

	err = json.Unmarshal([]byte(output), &shards)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal mdlog status output: %w", err)
	}

	return shards, nil
}

// GetRgwDatalogStatus fetches the data log head for every shard. Point it
// at the source zone to check progress syncing from that zone. A failing
// command returns nil; a successful one always returns a slice, empty log
// or not.
func GetRgwDatalogStatus(cluster string, client string) ([]RgwLogShard, error) {
	shards := []RgwLogShard{}

	output, err := radosgwAdminRunRemote(cluster, client, "datalog", "status")
	if err != nil {
		logger.Warnf("REPRGW: failed datalog status operation: %v", err)
		return nil, nil
	}

	err = json.Unmarshal([]byte(output), &shards)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal datalog status output: %w", err)
	}

	return shards, nil
}

// RgwSyncVerdict answers one question: has this zone caught up with its
// peer? It compares local sync markers against the peer's log heads using
// the same per-shard rule radosgw-admin applies.
//
// FullSyncShards is the shard count the zone reports minus the shards
// confirmed incremental, so a shard missing from the response counts the
// same as one still doing its first full copy and cannot read as caught
// up. This matches upstream's own total_behind arithmetic.
//
// CaughtUp also requires the zone to be actively syncing. A master, and a
// secondary that has not started, both report "init" with no markers, and
// neither is caught up. Skip this call entirely for a master rather than
// reading the resulting false as behind.
//
// PeriodMismatch, PeerLogUnavailable and LocalUnavailable all mean the
// comparison could not be made at all - the first two because the peer's
// side could not be used, the last because this zone's own markers were
// never read - so none of them is a claim about how far behind the zone
// is. LocalUnavailable is never set by the compute functions, which are
// only called with a local status that was actually read; the handler sets
// it in their place when the local read failed.
//
// Known gaps: a shard the peer does not report is logged and skipped
// rather than counted, as upstream also does; log trimming can briefly
// make a level shard look behind; and a data shard busy retrying failed
// objects still reads as caught up, because reading that state costs one
// radosgw-admin call per shard.
type RgwSyncVerdict struct {
	CaughtUp           bool
	BehindShards       []int
	FullSyncShards     int
	PeriodMismatch     bool
	PeerLogUnavailable bool
	LocalUnavailable   bool
}

// ComputeRgwMetadataSyncVerdict compares a secondary's metadata markers
// with the master's mdlog heads. A secondary on an older realm period is
// reported as PeriodMismatch without comparing shards, as upstream does.
func ComputeRgwMetadataSyncVerdict(local RgwMetadataSyncStatus, masterLog []RgwLogShard, currentPeriod string) RgwSyncVerdict {
	verdict := RgwSyncVerdict{}

	if local.Info.Period != "" && currentPeriod != "" && local.Info.Period != currentPeriod {
		verdict.PeriodMismatch = true
		return verdict
	}

	// A nil peer log means the fetch failed - the wrapper returns a
	// non-nil slice even for an empty log - so there is nothing to
	// compare against and no basis for saying caught up.
	if masterLog == nil {
		verdict.PeerLogUnavailable = true
		return verdict
	}

	numIncremental := 0
	for _, shard := range local.Markers {
		if shard.Val.State != RgwMetadataSyncStateIncremental {
			continue
		}
		numIncremental++
		// The key indexes the peer log, so both ends of its range are
		// checked here rather than trusted from the parse step: this
		// function is exported and takes the struct directly, so a
		// caller that did not come through GetRgwMetadataSyncStatus
		// never ran validateRgwSyncShards. The marker comparison below
		// is a string comparison on purpose - see RgwLogShard.Marker.
		if shard.Key < 0 || shard.Key >= len(masterLog) {
			logger.Warnf("REPRGW: metadata shard key %d is out of range for the peer mdlog (peer reported %d shard(s)); its sync state cannot be confirmed", shard.Key, len(masterLog))
		} else if masterLog[shard.Key].Marker > shard.Val.Marker {
			verdict.BehindShards = append(verdict.BehindShards, shard.Key)
		}
	}

	verdict.FullSyncShards = local.Info.NumShards - numIncremental
	if verdict.FullSyncShards < 0 {
		verdict.FullSyncShards = 0
	}
	verdict.CaughtUp = local.Info.Status == rgwSyncStateSync &&
		len(verdict.BehindShards) == 0 && verdict.FullSyncShards == 0
	return verdict
}

// ComputeRgwDataSyncVerdict compares local data sync markers for one source
// zone with that source's datalog heads.
func ComputeRgwDataSyncVerdict(local RgwDataSyncStatus, sourceLog []RgwLogShard) RgwSyncVerdict {
	verdict := RgwSyncVerdict{}

	if sourceLog == nil {
		verdict.PeerLogUnavailable = true
		return verdict
	}

	numIncremental := 0
	for _, shard := range local.Markers {
		if shard.Val.Status != "incremental-sync" {
			continue
		}
		numIncremental++
		// Both ends of the key range are checked for the same reason as
		// in ComputeRgwMetadataSyncVerdict, and the marker comparison is
		// likewise a deliberate string comparison.
		if shard.Key < 0 || shard.Key >= len(sourceLog) {
			logger.Warnf("REPRGW: data shard key %d is out of range for the source datalog (source reported %d shard(s)); its sync state cannot be confirmed", shard.Key, len(sourceLog))
		} else if sourceLog[shard.Key].Marker > shard.Val.Marker {
			verdict.BehindShards = append(verdict.BehindShards, shard.Key)
		}
	}

	verdict.FullSyncShards = local.Info.NumShards - numIncremental
	if verdict.FullSyncShards < 0 {
		verdict.FullSyncShards = 0
	}
	verdict.CaughtUp = local.Info.Status == rgwSyncStateSync &&
		len(verdict.BehindShards) == 0 && verdict.FullSyncShards == 0
	return verdict
}
