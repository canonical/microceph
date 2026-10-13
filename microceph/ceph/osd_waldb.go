package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/units"
	"github.com/spf13/afero"
	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/dsl"
	"github.com/canonical/microceph/microceph/logger"
)

type plannedAuxPartition struct {
	Kind           string
	ParentPath     string
	Partition      uint64
	SizeBytes      uint64
	PartUUID       string
	InitializeGPT  bool
	ResetBeforeUse bool
}

type plannedOSDProvision struct {
	OSDPath string
	WAL     *plannedAuxPartition
	DB      *plannedAuxPartition
}

type dslProvisionPlan struct {
	OSDs     []plannedOSDProvision
	Warnings []string
}

type generatedAuxDevice struct {
	ParentPath string `json:"parent_path"`
	Partition  uint64 `json:"partition"`
	PartUUID   string `json:"part_uuid,omitempty"`
	Encrypted  bool   `json:"encrypted"`
}

type generatedAuxDevicesManifest struct {
	WAL *generatedAuxDevice `json:"wal,omitempty"`
	DB  *generatedAuxDevice `json:"db,omitempty"`
}

type localAuxDiskUsage struct {
	HasData bool
	HasAux  bool
}

var createPlannedAuxPartitionFn = func(m *OSDManager, plan *plannedAuxPartition) (string, error) {
	return m.createPlannedAuxPartition(plan)
}

var doAddOSDWithStorageFn = func(m *OSDManager, ctx context.Context, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter, storage *api.ResourcesStorage, generatedAux *generatedAuxDevicesManifest) error {
	return m.doAddOSDWithStorage(ctx, data, wal, db, storage, generatedAux)
}

func intersectPathSets(a map[string]struct{}, b map[string]struct{}) []string {
	var out []string
	for path := range a {
		_, ok := b[path]
		if ok {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func buildResetBeforeUseWarnings(kind string, resetBeforeUse map[string]bool) []string {
	if len(resetBeforeUse) == 0 {
		return nil
	}

	paths := make([]string, 0, len(resetBeforeUse))
	for path, reset := range resetBeforeUse {
		if reset {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	warnings := make([]string, 0, len(paths))
	for _, path := range paths {
		warnings = append(warnings, fmt.Sprintf("%s carrier %s will be wiped/reset before partitioning", kind, path))
	}
	return warnings
}

func plannedAuxPartitionSummary(plan *plannedAuxPartition) string {
	if plan == nil {
		return "none"
	}

	action := "new"
	if plan.ResetBeforeUse {
		action = "reset"
	} else if !plan.InitializeGPT {
		action = "append"
	}

	return fmt.Sprintf("%s parent=%s part=%d size=%s action=%s", strings.ToUpper(plan.Kind), plan.ParentPath, plan.Partition, formatBytesIEC(int64(plan.SizeBytes)), action)
}

func plannedOSDProvisionSummary(planned plannedOSDProvision) string {
	return fmt.Sprintf("osd=%s wal=[%s] db=[%s]", planned.OSDPath, plannedAuxPartitionSummary(planned.WAL), plannedAuxPartitionSummary(planned.DB))
}

// planAuxiliaryPartitionsDetailed spreads WAL/DB partitions across the eligible
// carrier set in a deterministic way.
//
// It prefers carriers with fewer existing partitions first, then uses the stable
// device path as a tie-breaker. If a carrier must be reset before reuse, only the
// first partition planned on that carrier keeps ResetBeforeUse=true; subsequent
// partitions on the same carrier append to the freshly recreated partition table.
func planAuxiliaryPartitionsDetailed(osdPaths []string, carriers []api.ResourcesStorageDisk, resetBeforeUse map[string]bool, sizeBytes uint64, kind string) ([]*plannedAuxPartition, error) {
	if len(osdPaths) == 0 || len(carriers) == 0 {
		return make([]*plannedAuxPartition, len(osdPaths)), nil
	}

	states := make([]plannedCarrierState, len(carriers))
	for i, disk := range carriers {
		path := dsl.GetDevicePath(disk)
		used := diskUsedBytes(disk)
		remaining := uint64(0)
		partitionNo := nextPartitionNumber(disk)
		partitionCnt := len(disk.Partitions)
		reset := resetBeforeUse[path]
		if reset {
			remaining = disk.Size
			partitionNo = 1
			partitionCnt = 0
			disk.Partitions = nil // A reset discards all previously occupied slots.
		} else if disk.Size > used {
			remaining = disk.Size - used
		}
		states[i] = plannedCarrierState{
			Path:           path,
			Disk:           disk,
			PartitionNo:    partitionNo,
			PartitionCnt:   partitionCnt,
			RemainingSize:  remaining,
			ResetBeforeUse: reset,
		}
	}

	out := make([]*plannedAuxPartition, len(osdPaths))
	for i := range osdPaths {
		choice := -1
		for idx := range states {
			if states[idx].RemainingSize < sizeBytes {
				continue
			}
			if choice == -1 || states[idx].PartitionCnt < states[choice].PartitionCnt ||
				(states[idx].PartitionCnt == states[choice].PartitionCnt && states[idx].Path < states[choice].Path) {
				choice = idx
			}
		}
		if choice == -1 {
			return nil, fmt.Errorf("insufficient capacity for %s partitions of size %s", strings.ToUpper(kind), formatBytesIEC(int64(sizeBytes)))
		}
		out[i] = &plannedAuxPartition{
			Kind:           kind,
			ParentPath:     states[choice].Path,
			Partition:      states[choice].PartitionNo,
			SizeBytes:      sizeBytes,
			ResetBeforeUse: states[choice].ResetBeforeUse,
			// Slot 1 can be a hole before a live sibling; it does not imply an empty disk.
			InitializeGPT: states[choice].PartitionCnt == 0,
		}
		logger.Debugf("Planned %s partition for OSD %s: %s", strings.ToUpper(kind), osdPaths[i], plannedAuxPartitionSummary(out[i]))
		states[choice].RemainingSize -= sizeBytes
		states[choice].PartitionCnt++
		states[choice].PartitionNo++
		for {
			occupied := false
			for _, part := range states[choice].Disk.Partitions {
				if part.Partition == states[choice].PartitionNo {
					occupied = true
					break
				}
			}
			if !occupied {
				break
			}
			states[choice].PartitionNo++
		}
		states[choice].ResetBeforeUse = false
	}

	return out, nil
}

func (m *OSDManager) buildDSLProvisionPlan(ctx context.Context, req types.DisksPost) (*dslProvisionPlan, error) {
	logger.Infof("Building DSL provision plan for osd_match=%q wal_match=%q db_match=%q", req.OSDMatch, req.WALMatch, req.DBMatch)

	osdResult, err := m.matchOSDDisksWithDSL(ctx, req.OSDMatch)
	if err != nil {
		return nil, err
	}
	if osdResult.ValidationError != "" {
		return nil, fmt.Errorf("%s", osdResult.ValidationError)
	}

	plan := &dslProvisionPlan{}
	if len(osdResult.MatchedDisks) == 0 {
		logger.Infof("DSL provision plan has no OSD matches for expression %q", req.OSDMatch)
		plan.Warnings = append(plan.Warnings, "WAL/DB settings ignored because no new OSDs are being added")
		return plan, nil
	}

	osdPaths := make([]string, len(osdResult.MatchedDisks))
	plan.OSDs = make([]plannedOSDProvision, len(osdResult.MatchedDisks))
	for i, disk := range osdResult.MatchedDisks {
		path := dsl.GetDevicePath(disk)
		osdPaths[i] = path
		plan.OSDs[i] = plannedOSDProvision{OSDPath: path}
	}
	logger.Infof("DSL provision plan matched %d OSD device(s): %s", len(osdPaths), strings.Join(osdPaths, ", "))
	osdPathSet := buildPathSet(osdResult.MatchedDisks)

	var walMatches, dbMatches []api.ResourcesStorageDisk
	walResetBeforeUse := map[string]bool{}
	dbResetBeforeUse := map[string]bool{}
	if req.WALMatch != "" {
		walMatches, walResetBeforeUse, err = m.matchAuxiliaryDisksWithDSL(ctx, req.WALMatch, req.WALWipe)
		if err != nil {
			return nil, err
		}
		if len(walMatches) == 0 {
			logger.Infof("WAL DSL expression %q resolved to no eligible carriers", req.WALMatch)
			plan.Warnings = append(plan.Warnings, "WAL match expression resolved to no devices; proceeding without WAL")
		} else {
			plan.Warnings = append(plan.Warnings, buildResetBeforeUseWarnings("WAL", walResetBeforeUse)...)
		}
	}
	if req.DBMatch != "" {
		dbMatches, dbResetBeforeUse, err = m.matchAuxiliaryDisksWithDSL(ctx, req.DBMatch, req.DBWipe)
		if err != nil {
			return nil, err
		}
		if len(dbMatches) == 0 {
			logger.Infof("DB DSL expression %q resolved to no eligible carriers", req.DBMatch)
			plan.Warnings = append(plan.Warnings, "DB match expression resolved to no devices; proceeding without DB")
		} else {
			plan.Warnings = append(plan.Warnings, buildResetBeforeUseWarnings("DB", dbResetBeforeUse)...)
		}
	}

	walPathSet := buildPathSet(walMatches)
	dbPathSet := buildPathSet(dbMatches)
	if len(walPathSet) > 0 {
		logger.Infof("Eligible WAL carriers: %s", strings.Join(pathSetToSlice(walPathSet), ", "))
	}
	resetPaths := trueBoolMapKeys(walResetBeforeUse)
	if len(resetPaths) > 0 {
		logger.Infof("WAL carriers requiring reset before use: %s", strings.Join(resetPaths, ", "))
	}
	if len(dbPathSet) > 0 {
		logger.Infof("Eligible DB carriers: %s", strings.Join(pathSetToSlice(dbPathSet), ", "))
	}
	resetPaths = trueBoolMapKeys(dbResetBeforeUse)
	if len(resetPaths) > 0 {
		logger.Infof("DB carriers requiring reset before use: %s", strings.Join(resetPaths, ", "))
	}
	overlap := intersectPathSets(osdPathSet, walPathSet)
	if len(overlap) > 0 {
		logger.Warnf("Refusing DSL provision plan because OSD and WAL match sets overlap: %s", strings.Join(overlap, ", "))
		return nil, fmt.Errorf("OSD and WAL match sets overlap: %s", strings.Join(overlap, ", "))
	}
	overlap = intersectPathSets(osdPathSet, dbPathSet)
	if len(overlap) > 0 {
		logger.Warnf("Refusing DSL provision plan because OSD and DB match sets overlap: %s", strings.Join(overlap, ", "))
		return nil, fmt.Errorf("OSD and DB match sets overlap: %s", strings.Join(overlap, ", "))
	}
	overlap = intersectPathSets(walPathSet, dbPathSet)
	if len(overlap) > 0 {
		logger.Warnf("Refusing DSL provision plan because WAL and DB match sets overlap: %s", strings.Join(overlap, ", "))
		return nil, fmt.Errorf("WAL and DB match sets overlap: %s", strings.Join(overlap, ", "))
	}

	if req.WALMatch != "" && len(walMatches) > 0 {
		sizeBytes, err := units.ParseByteSizeString(req.WALSize)
		if err != nil {
			return nil, fmt.Errorf("invalid WAL size: %v", err)
		}
		walPlan, err := planAuxiliaryPartitionsDetailed(osdPaths, walMatches, walResetBeforeUse, uint64(sizeBytes), "wal")
		if err != nil {
			return nil, err
		}
		for i := range plan.OSDs {
			plan.OSDs[i].WAL = walPlan[i]
		}
	}

	if req.DBMatch != "" && len(dbMatches) > 0 {
		sizeBytes, err := units.ParseByteSizeString(req.DBSize)
		if err != nil {
			return nil, fmt.Errorf("invalid DB size: %v", err)
		}
		dbPlan, err := planAuxiliaryPartitionsDetailed(osdPaths, dbMatches, dbResetBeforeUse, uint64(sizeBytes), "db")
		if err != nil {
			return nil, err
		}
		for i := range plan.OSDs {
			plan.OSDs[i].DB = dbPlan[i]
		}
	}

	for _, planned := range plan.OSDs {
		logger.Debugf("Built DSL provision plan row: %s", plannedOSDProvisionSummary(planned))
	}
	logger.Infof("Built DSL provision plan for %d OSD(s)", len(plan.OSDs))
	return plan, nil
}

func dryRunResponseFromProvisionPlan(plan *dslProvisionPlan) types.DiskAddResponse {
	resp := types.DiskAddResponse{Warnings: append([]string(nil), plan.Warnings...)}
	resp.DryRunPlan = make([]types.DryRunOSDPlan, len(plan.OSDs))
	for i, osd := range plan.OSDs {
		resp.DryRunPlan[i].OSDPath = osd.OSDPath
		if osd.WAL != nil {
			resp.DryRunPlan[i].WAL = &types.DryRunPartitionPlan{
				Kind:           osd.WAL.Kind,
				ParentPath:     osd.WAL.ParentPath,
				Partition:      osd.WAL.Partition,
				Size:           formatBytesIEC(int64(osd.WAL.SizeBytes)),
				ResetBeforeUse: osd.WAL.ResetBeforeUse,
			}
		}
		if osd.DB != nil {
			resp.DryRunPlan[i].DB = &types.DryRunPartitionPlan{
				Kind:           osd.DB.Kind,
				ParentPath:     osd.DB.ParentPath,
				Partition:      osd.DB.Partition,
				Size:           formatBytesIEC(int64(osd.DB.SizeBytes)),
				ResetBeforeUse: osd.DB.ResetBeforeUse,
			}
		}
	}
	return resp
}

func partitionSizeMiB(sizeBytes uint64) uint64 {
	const mib = 1024 * 1024
	return (sizeBytes + mib - 1) / mib
}

func (m *OSDManager) initializeGPT(parentPath string) error {
	cmd := fmt.Sprintf("printf 'label: gpt\n' | sfdisk --lock=no %q", parentPath)
	_, err := m.runner.RunCommand("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("failed to initialize GPT on %s: %w", parentPath, err)
	}
	return nil
}

func (m *OSDManager) createPartition(parentPath string, sectors uint64) error {
	// A relative MiB size can be silently shrunk to fit. An exact sector count
	// lets sfdisk choose a suitable free extent (including holes) or fail.
	// The caller holds flock, so sfdisk must not try to acquire another lock.
	cmd := fmt.Sprintf("printf ',%d\n' | sfdisk --lock=no --append --force --no-reread --no-tell-kernel %q", sectors, parentPath)
	_, err := m.runner.RunCommand("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("failed to create partition on %s: %w", parentPath, err)
	}
	return nil
}

func partitionPathFromParentPath(parentPath string, partition uint64) string {
	if strings.Contains(parentPath, "/dev/disk/by-id/") || strings.Contains(parentPath, "/dev/disk/by-path/") {
		return fmt.Sprintf("%s-part%d", parentPath, partition)
	}
	// Numeric kernel disk names (NVMe, loop, mmcblk) need a 'p' separator.
	if len(parentPath) > 0 && parentPath[len(parentPath)-1] >= '0' && parentPath[len(parentPath)-1] <= '9' {
		return fmt.Sprintf("%sp%d", parentPath, partition)
	}
	return fmt.Sprintf("%s%d", parentPath, partition)
}

func partitionPathCandidates(parentPath string, partition uint64) []string {
	seen := map[string]struct{}{}
	parents := []string{parentPath, resolvePathBestEffort(parentPath)}
	candidates := make([]string, 0, len(parents))
	for _, parent := range parents {
		if parent == "" {
			continue
		}
		candidate := partitionPathFromParentPath(parent, partition)
		_, ok := seen[candidate]
		if ok {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func resolvePathBestEffort(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func auxiliaryDiskPathCandidates(disk api.ResourcesStorageDisk) []string {
	candidates := []string{common.GetDevicePath(&disk), resolvePathBestEffort(common.GetDevicePath(&disk))}
	for _, part := range disk.Partitions {
		raw := fmt.Sprintf("/dev/%s", part.ID)
		candidates = append(candidates, raw, resolvePathBestEffort(raw))
		if disk.DeviceID != "" {
			byID := fmt.Sprintf("/dev/disk/by-id/%s-part%d", disk.DeviceID, part.Partition)
			candidates = append(candidates, byID, resolvePathBestEffort(byID))
		}
		if disk.DevicePath != "" {
			byPath := fmt.Sprintf("/dev/disk/by-path/%s-part%d", disk.DevicePath, part.Partition)
			candidates = append(candidates, byPath, resolvePathBestEffort(byPath))
		}
	}
	return candidates
}

func (m *OSDManager) diskOrAnyPartitionMounted(disk api.ResourcesStorageDisk) (bool, error) {
	mounted, err := m.mountChecker.IsMounted(common.GetDevicePath(&disk))
	if err != nil || mounted {
		return mounted, err
	}
	for _, part := range disk.Partitions {
		partPath := fmt.Sprintf("/dev/%s", part.ID)
		mounted, err = m.mountChecker.IsMounted(partPath)
		if err != nil || mounted {
			return mounted, err
		}
	}
	return false, nil
}

func (m *OSDManager) collectLocalAuxDiskUsage(storage *api.ResourcesStorage) (map[string]localAuxDiskUsage, error) {
	lookup := map[string]string{}
	usage := map[string]localAuxDiskUsage{}
	for _, disk := range storage.Disks {
		parent := common.GetDevicePath(&disk)
		usage[parent] = localAuxDiskUsage{}
		for _, candidate := range auxiliaryDiskPathCandidates(disk) {
			lookup[candidate] = parent
		}
	}

	baseDir := filepath.Join(constants.GetPathConst().DataPath, "osd")
	osdDirs, err := afero.ReadDir(m.fs, baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return usage, nil
		}
		return nil, fmt.Errorf("failed to inspect local ceph device usage: %w", err)
	}

	for _, osdDir := range osdDirs {
		if !osdDir.IsDir() || !strings.HasPrefix(osdDir.Name(), "ceph-") {
			continue
		}
		for _, link := range common.CephOSDDeviceLinks() {
			targetPath := filepath.Join(baseDir, osdDir.Name(), link.Name)
			resolved := resolvePathBestEffort(targetPath)
			parent, ok := lookup[resolved]
			if !ok {
				continue
			}
			entry := usage[parent]
			if link.Auxiliary {
				entry.HasAux = true
			} else {
				entry.HasData = true
			}
			usage[parent] = entry
		}
	}

	return usage, nil
}

func generatedAuxManifestPath(osdDataPath string) string {
	return filepath.Join(osdDataPath, "generated-aux-devices.json")
}

func (m *OSDManager) writeGeneratedAuxManifest(osdDataPath string, manifest *generatedAuxDevicesManifest) error {
	if manifest == nil || (manifest.WAL == nil && manifest.DB == nil) {
		return nil
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal generated aux manifest: %w", err)
	}

	manifestPath := generatedAuxManifestPath(osdDataPath)
	// A failed rewrite must leave the remaining cleanup entries readable.
	tmpPath := manifestPath + ".tmp"
	defer m.fs.Remove(tmpPath)
	err = afero.WriteFile(m.fs, tmpPath, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write generated aux manifest: %w", err)
	}
	err = m.fs.Rename(tmpPath, manifestPath)
	if err != nil {
		return fmt.Errorf("failed to replace generated aux manifest: %w", err)
	}

	logger.Infof("Persisted generated auxiliary-device manifest at %s", manifestPath)
	return nil
}

func (m *OSDManager) persistGeneratedAuxManifest(osdDataPath string, manifest *generatedAuxDevicesManifest) error {
	if manifest == nil || (manifest.WAL == nil && manifest.DB == nil) {
		err := m.fs.Remove(generatedAuxManifestPath(osdDataPath))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove generated aux manifest: %w", err)
		}
		return nil
	}

	return m.writeGeneratedAuxManifest(osdDataPath, manifest)
}

func (m *OSDManager) readGeneratedAuxManifest(osdDataPath string) (*generatedAuxDevicesManifest, error) {
	manifestPath := generatedAuxManifestPath(osdDataPath)
	data, err := afero.ReadFile(m.fs, manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read generated aux manifest: %w", err)
	}

	var manifest generatedAuxDevicesManifest
	err = json.Unmarshal(data, &manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to parse generated aux manifest: %w", err)
	}
	logger.Debugf("Loaded generated auxiliary-device manifest from %s", manifestPath)
	return &manifest, nil
}

func auxDeviceMapperName(kind string, osdID int64) string {
	suffix := "." + kind
	return fmt.Sprintf("luksosd%s-%d", suffix, osdID)
}

func (m *OSDManager) closeEncryptedAuxDevice(kind string, osdID int64) error {
	mapperName := auxDeviceMapperName(kind, osdID)
	mapperPath := filepath.Join("/dev/mapper", mapperName)
	_, err := m.fs.Stat(mapperPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect encrypted aux device %s: %w", mapperPath, err)
	}

	_, err = m.runner.RunCommand("cryptsetup", "close", mapperName)
	if err != nil {
		return fmt.Errorf("failed to close encrypted %s device %s: %w", kind, mapperName, err)
	}
	return nil
}

// auxPartitionTable is an on-disk snapshot, not udev's possibly stale inventory.
// The same snapshot supplies allocation geometry and cleanup ownership.
type auxPartitionTable struct {
	Label      string         `json:"label"`
	SectorSize uint64         `json:"sectorsize"`
	Partitions []auxPartition `json:"partitions"`
}

type auxPartition struct {
	Node string `json:"node"`
	UUID string `json:"uuid"`
	Size uint64 `json:"size"`
}

func (m *OSDManager) readAuxPartitionTable(parentPath string) (*auxPartitionTable, error) {
	// sfdisk uses the same exit status for a blank disk and an I/O error.
	// Pin its diagnostic locale and recognize only the explicit no-table case.
	output, err := m.runner.RunCommand("env", "LC_ALL=C", "sfdisk", "--json", parentPath)
	if err != nil {
		if strings.Contains(err.Error(), "does not contain a recognized partition table") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read partition table on %s: %w", parentPath, err)
	}
	var result struct {
		Table auxPartitionTable `json:"partitiontable"`
	}
	err = json.Unmarshal([]byte(output), &result)
	if err != nil {
		return nil, fmt.Errorf("invalid partition table on %s: %w", parentPath, err)
	}
	if result.Table.Label == "" || result.Table.SectorSize == 0 {
		return nil, fmt.Errorf("incomplete partition table on %s", parentPath)
	}
	return &result.Table, nil
}

func (table *auxPartitionTable) partition(parentPath string, number uint64) *auxPartition {
	if table == nil {
		return nil
	}
	node := partitionPathFromParentPath(parentPath, number)
	for i := range table.Partitions {
		if table.Partitions[i].Node == node {
			return &table.Partitions[i]
		}
	}
	return nil
}

const (
	kernelPartitionDeleteAttempts = 6
	kernelPartitionDeleteBackoff  = 200 * time.Millisecond
)

var partxDeleteSleepFunc = time.Sleep

func (m *OSDManager) deleteKernelPartitionEntry(parentPath string, partition uint64) error {
	var err error
	partitionRange := fmt.Sprintf("%d:%d", partition, partition)
	for attempt := 1; attempt <= kernelPartitionDeleteAttempts; attempt++ {
		// partx treats an already-absent kernel entry as success. A missing
		// /dev link alone must never turn a real deletion error into success.
		_, err = m.runner.RunCommand("partx", "-v", "-d", "--nr", partitionRange, parentPath)
		if err == nil {
			return nil
		}
		if attempt < kernelPartitionDeleteAttempts {
			partxDeleteSleepFunc(kernelPartitionDeleteBackoff)
		}
	}
	return fmt.Errorf("failed to remove kernel partition %d on %s after %d attempts: %w", partition, parentPath, kernelPartitionDeleteAttempts, err)
}

// withExclusiveCarrierLock excludes udev probes, not just other MicroCeph calls.
// The API mutex already serializes add/remove, including planning and rollback.
// Resolve the carrier once so every command uses the node whose lock we hold.
func withExclusiveCarrierLock(parentPath string, operation func(string) error) error {
	parentPath, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve carrier: %w", err)
	}
	disk, err := os.OpenFile(parentPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open carrier %s: %w", parentPath, err)
	}
	// Closing a writable descriptor both releases flock and prompts udev to
	// reprobe the final state. Never wait for udev links inside the operation.
	defer disk.Close()
	deadline := time.Now().Add(45 * time.Second) // Allow another 30-second zap to finish.
	for {
		// LOCK_NB returns immediately on contention so we can check the deadline;
		// a blocking flock could wait indefinitely before reaching that check.
		err = unix.Flock(int(disk.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return operation(parentPath)
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return fmt.Errorf("failed to lock carrier %s: %w", parentPath, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out locking carrier %s: %w", parentPath, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// deletePartition requires the carrier lock and established ownership, either
// a matching recorded UUID or a slot just created while this lock was held.
// Removing the kernel entry first leaves GPT intact if a holder stays busy.
func (m *OSDManager) deletePartition(parentPath string, partition uint64) error {
	err := m.deleteKernelPartitionEntry(parentPath, partition)
	if err != nil {
		return err
	}
	_, err = m.runner.RunCommand("sfdisk", "--delete", "--lock=no", "--no-reread", "--no-tell-kernel", parentPath, strconv.FormatUint(partition, 10))
	if err != nil {
		return fmt.Errorf("failed to delete GPT partition %d on %s: %w", partition, parentPath, err)
	}
	// An external opener can recreate an entry between the first deletion
	// and the GPT write. This targeted, idempotent delete leaves siblings alone.
	return m.deleteKernelPartitionEntry(parentPath, partition)
}

func (m *OSDManager) cleanupGeneratedAuxDevice(_ context.Context, kind string, entry *generatedAuxDevice, osdID int64) error {
	if entry == nil {
		return nil
	}
	_, err := m.fs.Stat(entry.ParentPath)
	missing := entry.ParentPath == "" || os.IsNotExist(err)
	if err != nil && !missing {
		return err
	}
	// Auxiliary allocation can fail before an OSD ID exists. In that case
	// there is no mapper to close; zero is a real OSD ID, not a sentinel.
	if entry.Encrypted && osdID >= 0 {
		err = m.closeEncryptedAuxDevice(kind, osdID)
		if err != nil {
			_, statErr := m.fs.Stat(entry.ParentPath)
			missing = missing || os.IsNotExist(statErr)
			if !missing {
				return err
			}
			logger.Warnf("Failed to close encrypted %s mapper for missing carrier %s: %v; manual mapper cleanup may be required", kind, entry.ParentPath, err)
		}
	}
	// The mapper can outlive its carrier. Try to close it above, but a dead
	// carrier must not prevent removal even if the mapper cannot be released.
	if missing {
		logger.Warnf("Generated %s carrier %q is missing; discarding its cleanup entry", kind, entry.ParentPath)
		return nil
	}
	if entry.PartUUID == "" {
		// Legacy manifests cannot prove ownership after a slot has been reused.
		logger.Warnf("Generated %s partition %d on %s has no recorded PARTUUID; leaving it for manual cleanup", kind, entry.Partition, entry.ParentPath)
		return nil
	}
	err = m.lockCarrierFunc(entry.ParentPath, func(parentPath string) error {
		table, err := m.readAuxPartitionTable(parentPath)
		if err != nil {
			return err
		}
		part := table.partition(parentPath, entry.Partition)
		if part == nil {
			// A previous attempt may have deleted GPT before its final kernel
			// update or manifest write. Never zap a node without a matching GPT.
			return m.deleteKernelPartitionEntry(parentPath, entry.Partition)
		}
		if part.UUID == "" {
			return fmt.Errorf("partition %d on %s has no PARTUUID; refusing cleanup", entry.Partition, parentPath)
		}
		if !strings.EqualFold(part.UUID, entry.PartUUID) {
			logger.Warnf("Generated %s partition %d on %s was replaced; leaving UUID %s untouched", kind, entry.Partition, parentPath, part.UUID)
			return nil
		}
		// Old manifests may contain partition_path, which is deliberately not
		// decoded: raw names can be reassigned. Use the locked carrier's child.
		partitionPath := partitionPathFromParentPath(parentPath, entry.Partition)
		_, err = m.fs.Stat(partitionPath)
		if err == nil {
			// Zap is best effort, but bound it to one attempt while holding flock.
			err = m.timeoutWipe(partitionPath)
			if err != nil {
				logger.Warnf("Failed to wipe generated %s partition %s: %v", kind, partitionPath, err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		return m.deletePartition(parentPath, entry.Partition)
	})
	if err != nil {
		// Hot-unplug between the initial stat and any later command must not
		// strand the OSD. An existing but unreadable carrier remains an error.
		_, statErr := m.fs.Stat(entry.ParentPath)
		if os.IsNotExist(statErr) {
			logger.Warnf("Generated %s carrier %s disappeared; discarding its cleanup entry", kind, entry.ParentPath)
			return nil
		}
	}
	return err
}

func (m *OSDManager) cleanupGeneratedAuxEntries(ctx context.Context, manifest *generatedAuxDevicesManifest, osdID int64) error {
	if manifest == nil {
		return nil
	}
	err := m.cleanupGeneratedAuxDevice(ctx, "wal", manifest.WAL, osdID)
	if err != nil {
		return err
	}
	err = m.cleanupGeneratedAuxDevice(ctx, "db", manifest.DB, osdID)
	if err != nil {
		return err
	}
	return nil
}

func (m *OSDManager) cleanupGeneratedAuxDevices(ctx context.Context, osdDataPath string, osdID int64) error {
	manifest, err := m.readGeneratedAuxManifest(osdDataPath)
	if err != nil {
		return err
	}
	if manifest == nil {
		return nil
	}

	if manifest.WAL != nil {
		err := m.cleanupGeneratedAuxDevice(ctx, "wal", manifest.WAL, osdID)
		if err != nil {
			return err
		}
		manifest.WAL = nil
		err = m.persistGeneratedAuxManifest(osdDataPath, manifest)
		if err != nil {
			return err
		}
	}

	if manifest.DB != nil {
		err := m.cleanupGeneratedAuxDevice(ctx, "db", manifest.DB, osdID)
		if err != nil {
			return err
		}
		manifest.DB = nil
		err = m.persistGeneratedAuxManifest(osdDataPath, manifest)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *OSDManager) resolvePartitionStablePath(parentPath string, partition uint64) (string, error) {
	for _, candidate := range partitionPathCandidates(parentPath, partition) {
		exists, err := afero.Exists(m.fs, candidate)
		if err == nil && exists {
			return candidate, nil
		}
	}

	storage, err := m.getStorageWithRetry()
	if err == nil {
		for _, disk := range storage.Disks {
			if common.GetDevicePath(&disk) != parentPath {
				continue
			}
			for _, part := range disk.Partitions {
				if part.Partition != partition {
					continue
				}
				candidate := fmt.Sprintf("/dev/%s", part.ID)
				exists, statErr := afero.Exists(m.fs, candidate)
				if statErr == nil && exists {
					return candidate, nil
				}
			}
		}
	}

	return "", fmt.Errorf("partition %d on %s not found", partition, parentPath)
}

func (table *auxPartitionTable) checkAllocationSlot(parentPath string, plan *plannedAuxPartition) error {
	if table == nil || table.Label != "gpt" {
		return fmt.Errorf("carrier %s has no usable GPT", parentPath)
	}
	// Planning uses the storage inventory. Check its chosen slot against disk
	// before appending, so a stale inventory cannot bind us to someone else's slot.
	next := uint64(1)
	for table.partition(parentPath, next) != nil {
		next++
	}
	if next != plan.Partition {
		return fmt.Errorf("planned partition %d on %s does not match free slot %d", plan.Partition, parentPath, next)
	}
	return nil
}

func (m *OSDManager) createPlannedAuxPartition(plan *plannedAuxPartition) (string, error) {
	if plan == nil {
		return "", nil
	}

	logger.Infof("Creating planned auxiliary partition: %s", plannedAuxPartitionSummary(plan))
	err := m.lockCarrierFunc(plan.ParentPath, func(parentPath string) (retErr error) {
		table, err := m.readAuxPartitionTable(parentPath)
		if err != nil {
			return err
		}
		// API serialization spans discovery through provisioning. No reset
		// snapshots or reservations are needed; concurrent manual repartitioning
		// of MicroCeph carriers is not supported.
		if plan.ResetBeforeUse {
			err = m.timeoutWipe(parentPath)
			if err != nil {
				return fmt.Errorf("failed to reset carrier %s: %w", parentPath, err)
			}
		} else if plan.InitializeGPT && table != nil && len(table.Partitions) != 0 {
			return fmt.Errorf("carrier %s is no longer empty; refusing GPT initialization", parentPath)
		}
		if plan.InitializeGPT || plan.ResetBeforeUse {
			err = m.initializeGPT(parentPath)
			if err != nil {
				return err
			}
			table, err = m.readAuxPartitionTable(parentPath)
			if err != nil {
				return err
			}
		}
		err = table.checkAllocationSlot(parentPath, plan)
		if err != nil {
			return err
		}
		// A free GPT slot may still have an old kernel entry after an interrupted
		// removal. Refuse a busy stale entry before assigning this slot again.
		err = m.deleteKernelPartitionEntry(parentPath, plan.Partition)
		if err != nil {
			return err
		}
		sectors := (partitionSizeMiB(plan.SizeBytes)*1024*1024 + table.SectorSize - 1) / table.SectorSize
		err = m.createPartition(parentPath, sectors)
		if err != nil {
			return err
		}
		// No other MicroCeph operation can take this newly allocated slot while
		// we hold the lock. Even a failed UUID read must roll it back, rather
		// than leave a partition that no generated-device manifest records.
		defer func() {
			if retErr == nil {
				return
			}
			cleanupErr := m.deletePartition(parentPath, plan.Partition)
			if cleanupErr != nil {
				retErr = fmt.Errorf("%w (rollback of partition %d on %s failed: %v; manual cleanup required)", retErr, plan.Partition, parentPath, cleanupErr)
			} else {
				plan.PartUUID = ""
			}
		}()
		table, err = m.readAuxPartitionTable(parentPath)
		if err != nil {
			return err
		}
		part := table.partition(parentPath, plan.Partition)
		if part == nil || part.UUID == "" {
			return fmt.Errorf("cannot identify created partition %d on %s", plan.Partition, parentPath)
		}
		if part.Size != sectors {
			return fmt.Errorf("created partition %d on %s has %d sectors, requested %d", plan.Partition, parentPath, part.Size, sectors)
		}
		// Capture ownership before udev sees the new partition. A later removal
		// must not trust this slot number after it has been freed and reused.
		plan.PartUUID = part.UUID
		// Publish only this partition, and do not ignore a kernel update error:
		// an existing /dev node alone is not proof that the new extent is usable.
		_, err = m.runner.RunCommand("partx", "-u", "--nr", fmt.Sprintf("%d:%d", plan.Partition, plan.Partition), parentPath)
		if err != nil {
			return fmt.Errorf("failed to publish partition %d on %s: %w", plan.Partition, parentPath, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	// udev skips probes while flock is held; wait for its links only after close.

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		path, err := m.resolvePartitionStablePath(plan.ParentPath, plan.Partition)
		if err == nil {
			logger.Infof("Created %s partition %s for parent %s", strings.ToUpper(plan.Kind), path, plan.ParentPath)
			return path, nil
		}
		time.Sleep(1 * time.Second)
	}
	err = fmt.Errorf("timed out waiting for %s partition %d on %s to appear", plan.Kind, plan.Partition, plan.ParentPath)
	entry := &generatedAuxDevice{ParentPath: plan.ParentPath, Partition: plan.Partition, PartUUID: plan.PartUUID}
	cleanupErr := m.cleanupGeneratedAuxDevice(context.Background(), plan.Kind, entry, -1)
	if cleanupErr != nil {
		return "", fmt.Errorf("%w (rollback failed: %v; manual cleanup required)", err, cleanupErr)
	}
	plan.PartUUID = ""
	return "", err
}

func buildGeneratedAuxDiskParameter(plan *plannedAuxPartition, path string, encrypt bool, wipe bool) (*types.DiskParameter, *generatedAuxDevice) {
	if plan == nil {
		return nil, nil
	}

	return &types.DiskParameter{Path: path, Encrypt: encrypt, Wipe: wipe, SkipPristineCheck: true}, &generatedAuxDevice{
		ParentPath: plan.ParentPath,
		Partition:  plan.Partition,
		PartUUID:   plan.PartUUID,
		Encrypted:  encrypt,
	}
}

func (m *OSDManager) executeDSLProvisionPlan(ctx context.Context, plan *dslProvisionPlan, req types.DisksPost) types.DiskAddResponse {
	resp := types.DiskAddResponse{Warnings: append([]string(nil), plan.Warnings...)}
	if len(plan.OSDs) == 0 {
		return resp
	}

	logger.Infof("Executing DSL provision plan for %d OSD(s)", len(plan.OSDs))
	for _, planned := range plan.OSDs {
		logger.Infof("Executing DSL provision plan row: %s", plannedOSDProvisionSummary(planned))

		var walParam *types.DiskParameter
		var dbParam *types.DiskParameter
		var generatedAux *generatedAuxDevicesManifest

		dataStorage, err := m.getStorageWithRetry()
		if err != nil {
			logger.Errorf("Unable to list system disks before adding OSD %s: %v", planned.OSDPath, err)
			resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: fmt.Sprintf("unable to list system disks: %v", err)})
			break
		}

		if planned.WAL != nil {
			path, err := createPlannedAuxPartitionFn(m, planned.WAL)
			if err != nil {
				logger.Errorf("Failed creating planned WAL partition for OSD %s: %v", planned.OSDPath, err)
				reportErr := err.Error()
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: reportErr})
				break
			}
			var generatedWAL *generatedAuxDevice
			walParam, generatedWAL = buildGeneratedAuxDiskParameter(planned.WAL, path, req.WALEncrypt, req.WALWipe)
			logger.Infof("Prepared WAL partition %s for OSD %s (encrypt=%t wipe=%t)", path, planned.OSDPath, req.WALEncrypt, req.WALWipe)
			if generatedAux == nil {
				generatedAux = &generatedAuxDevicesManifest{}
			}
			generatedAux.WAL = generatedWAL
		}

		if planned.DB != nil {
			path, err := createPlannedAuxPartitionFn(m, planned.DB)
			if err != nil {
				logger.Errorf("Failed creating planned DB partition for OSD %s: %v", planned.OSDPath, err)
				reportErr := err.Error()
				if generatedAux != nil {
					cleanupErr := m.cleanupGeneratedAuxEntries(ctx, generatedAux, -1)
					if cleanupErr != nil {
						logger.Warnf("Automatic cleanup after DB partition creation failure for OSD %s also failed: %v", planned.OSDPath, cleanupErr)
						reportErr = fmt.Sprintf("%s (automatic cleanup also failed: %v)", reportErr, cleanupErr)
						resp.Warnings = append(resp.Warnings, "Automatic cleanup of generated WAL/DB partitions failed; manual cleanup may be required")
					}
				}
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: reportErr})
				break
			}
			var generatedDB *generatedAuxDevice
			dbParam, generatedDB = buildGeneratedAuxDiskParameter(planned.DB, path, req.DBEncrypt, req.DBWipe)
			logger.Infof("Prepared DB partition %s for OSD %s (encrypt=%t wipe=%t)", path, planned.OSDPath, req.DBEncrypt, req.DBWipe)
			if generatedAux == nil {
				generatedAux = &generatedAuxDevicesManifest{}
			}
			generatedAux.DB = generatedDB
		}

		logger.Infof("Adding OSD %s with planned auxiliary devices", planned.OSDPath)
		err = doAddOSDWithStorageFn(m, ctx, types.DiskParameter{Path: planned.OSDPath, Encrypt: req.Encrypt, Wipe: req.Wipe}, walParam, dbParam, dataStorage, generatedAux)
		if err != nil {
			logger.Errorf("Failed to add OSD %s using DSL provision plan: %v", planned.OSDPath, err)
			report := types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: err.Error()}
			resp.Reports = append(resp.Reports, report)
			break
		}

		logger.Infof("Successfully added OSD %s using DSL provision plan", planned.OSDPath)
		report := types.DiskAddReport{Path: planned.OSDPath, Report: "Success", Error: ""}
		resp.Reports = append(resp.Reports, report)
	}

	return resp
}
