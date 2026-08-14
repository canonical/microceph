package ceph

import (
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
)

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

// RgwSyncInfo is the info block shared by `metadata sync status` and
// `data sync status` output. Period and RealmEpoch are metadata-only.
type RgwSyncInfo struct {
	Status     string `json:"status"`
	NumShards  int    `json:"num_shards"`
	Period     string `json:"period"`
	RealmEpoch int    `json:"realm_epoch"`
}

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

// GetRgwMetadataSyncStatus fetches this zone's own metadata sync markers -
// local progress only, no peer contact. A non-empty cluster/client pair
// targets a remote cluster. A failing command yields a zero value and a
// nil error.
func GetRgwMetadataSyncStatus(cluster string, client string) (RgwMetadataSyncStatus, error) {
	envelope := rgwMetadataSyncEnvelope{}

	output, err := radosgwAdminRunRemote(cluster, client, "metadata", "sync", "status")
	if err != nil {
		logger.Warnf("REPRGW: failed metadata sync status operation: %v", err)
		return RgwMetadataSyncStatus{}, nil
	}

	err = json.Unmarshal([]byte(output), &envelope)
	if err != nil {
		return RgwMetadataSyncStatus{}, fmt.Errorf("cannot unmarshal metadata sync status output: %w", err)
	}

	return RgwMetadataSyncStatus{Info: envelope.SyncStatus.Info, Markers: envelope.SyncStatus.Markers}, nil
}

// GetRgwDataSyncStatus fetches this zone's own data sync markers for one
// source zone - local progress only, no contact with the source. A
// non-empty cluster/client pair targets a remote cluster. A failing
// command yields a zero value and a nil error.
func GetRgwDataSyncStatus(sourceZone string, cluster string, client string) (RgwDataSyncStatus, error) {
	envelope := rgwDataSyncEnvelope{}

	output, err := radosgwAdminRunRemote(cluster, client, "data", "sync", "status", "--source-zone", sourceZone)
	if err != nil {
		logger.Warnf("REPRGW: failed data sync status operation for source(%s): %v", sourceZone, err)
		return RgwDataSyncStatus{}, nil
	}

	err = json.Unmarshal([]byte(output), &envelope)
	if err != nil {
		return RgwDataSyncStatus{}, fmt.Errorf("cannot unmarshal data sync status output: %w", err)
	}

	return RgwDataSyncStatus{Info: envelope.SyncStatus.Info, Markers: envelope.SyncStatus.Markers}, nil
}

// RgwLogShard is one shard of `mdlog status` or `datalog status` output:
// the log head on the cluster that owns it. Array index is the shard id.
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
// Known gap: log trimming can briefly make a level shard look behind.
type RgwSyncVerdict struct {
	CaughtUp       bool
	BehindShards   []int
	FullSyncShards int
	PeriodMismatch bool
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

	for _, shard := range local.Markers {
		if shard.Val.State != RgwMetadataSyncStateIncremental {
			verdict.FullSyncShards++
			continue
		}
		if shard.Key < len(masterLog) && masterLog[shard.Key].Marker > shard.Val.Marker {
			verdict.BehindShards = append(verdict.BehindShards, shard.Key)
		}
	}

	verdict.CaughtUp = len(verdict.BehindShards) == 0 && verdict.FullSyncShards == 0
	return verdict
}

// ComputeRgwDataSyncVerdict compares local data sync markers for one source
// zone with that source's datalog heads.
func ComputeRgwDataSyncVerdict(local RgwDataSyncStatus, sourceLog []RgwLogShard) RgwSyncVerdict {
	verdict := RgwSyncVerdict{}

	for _, shard := range local.Markers {
		if shard.Val.Status != "incremental-sync" {
			verdict.FullSyncShards++
			continue
		}
		if shard.Key < len(sourceLog) && sourceLog[shard.Key].Marker > shard.Val.Marker {
			verdict.BehindShards = append(verdict.BehindShards, shard.Key)
		}
	}

	verdict.CaughtUp = len(verdict.BehindShards) == 0 && verdict.FullSyncShards == 0
	return verdict
}
