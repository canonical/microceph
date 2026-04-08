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
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/dsl"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/spf13/afero"
)

type plannedAuxPartition struct {
	Kind           string
	ParentPath     string
	Partition      uint64
	SizeBytes      uint64
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
	ParentPath    string `json:"parent_path"`
	Partition     uint64 `json:"partition"`
	PartitionPath string `json:"partition_path"`
	Encrypted     bool   `json:"encrypted"`
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
		if _, ok := b[path]; ok {
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
	} else if plan.Partition > 1 {
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
		}
		logger.Debugf("Planned %s partition for OSD %s: %s", strings.ToUpper(kind), osdPaths[i], plannedAuxPartitionSummary(out[i]))
		states[choice].RemainingSize -= sizeBytes
		states[choice].PartitionCnt++
		states[choice].PartitionNo++
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
		return nil, fmt.Errorf(osdResult.ValidationError)
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
	if resetPaths := trueBoolMapKeys(walResetBeforeUse); len(resetPaths) > 0 {
		logger.Infof("WAL carriers requiring reset before use: %s", strings.Join(resetPaths, ", "))
	}
	if len(dbPathSet) > 0 {
		logger.Infof("Eligible DB carriers: %s", strings.Join(pathSetToSlice(dbPathSet), ", "))
	}
	if resetPaths := trueBoolMapKeys(dbResetBeforeUse); len(resetPaths) > 0 {
		logger.Infof("DB carriers requiring reset before use: %s", strings.Join(resetPaths, ", "))
	}
	if overlap := intersectPathSets(osdPathSet, walPathSet); len(overlap) > 0 {
		logger.Warnf("Refusing DSL provision plan because OSD and WAL match sets overlap: %s", strings.Join(overlap, ", "))
		return nil, fmt.Errorf("OSD and WAL match sets overlap: %s", strings.Join(overlap, ", "))
	}
	if overlap := intersectPathSets(osdPathSet, dbPathSet); len(overlap) > 0 {
		logger.Warnf("Refusing DSL provision plan because OSD and DB match sets overlap: %s", strings.Join(overlap, ", "))
		return nil, fmt.Errorf("OSD and DB match sets overlap: %s", strings.Join(overlap, ", "))
	}
	if overlap := intersectPathSets(walPathSet, dbPathSet); len(overlap) > 0 {
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
	cmd := fmt.Sprintf("printf 'label: gpt\n' | sfdisk %q", parentPath)
	_, err := m.runner.RunCommand("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("failed to initialize GPT on %s: %w", parentPath, err)
	}
	return nil
}

func (m *OSDManager) createPartition(parentPath string, sizeBytes uint64) error {
	sizeMiB := partitionSizeMiB(sizeBytes)
	// Appending a new partition to a carrier that already has an in-use WAL/DB
	// partition is an expected DSL workflow. sfdisk refuses that by default, so
	// bypass its in-use preflight and let partx refresh the kernel view
	// afterwards.
	cmd := fmt.Sprintf("printf ',+%dMiB\n' | sfdisk --append --force --no-reread %q", sizeMiB, parentPath)
	_, err := m.runner.RunCommand("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("failed to create partition on %s: %w", parentPath, err)
	}
	return nil
}

func (m *OSDManager) refreshPartitionTable(parentPath string) {
	_, err := m.runner.RunCommand("partx", "-u", parentPath)
	if err == nil {
		return
	}
	_, _ = m.runner.RunCommand("blockdev", "--rereadpt", parentPath)
}

func partitionPathFromParentPath(parentPath string, partition uint64) string {
	if strings.Contains(parentPath, "/dev/disk/by-id/") || strings.Contains(parentPath, "/dev/disk/by-path/") {
		return fmt.Sprintf("%s-part%d", parentPath, partition)
	}
	if strings.Contains(parentPath, "nvme") {
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
		if _, ok := seen[candidate]; ok {
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
	if err := afero.WriteFile(m.fs, manifestPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write generated aux manifest: %w", err)
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
	if err := json.Unmarshal(data, &manifest); err != nil {
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
	if _, err := m.fs.Stat(mapperPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect encrypted aux device %s: %w", mapperPath, err)
	}

	_, err := m.runner.RunCommand("cryptsetup", "close", mapperName)
	if err != nil {
		return fmt.Errorf("failed to close encrypted %s device %s: %w", kind, mapperName, err)
	}
	return nil
}

func (m *OSDManager) deletePartition(parentPath string, partition uint64) error {
	if _, err := m.resolvePartitionStablePath(parentPath, partition); err != nil {
		logger.Infof("Partition %d on %s is already absent, skipping delete", partition, parentPath)
		return nil
	}

	_, err := m.runner.RunCommand("sfdisk", "--delete", parentPath, strconv.FormatUint(partition, 10))
	if err != nil {
		if _, resolveErr := m.resolvePartitionStablePath(parentPath, partition); resolveErr != nil {
			logger.Infof("Partition %d on %s disappeared despite delete error, treating as cleaned", partition, parentPath)
			return nil
		}
		return fmt.Errorf("failed to delete partition %d on %s: %w", partition, parentPath, err)
	}

	partitionRange := fmt.Sprintf("%d:%d", partition, partition)
	_, err = m.runner.RunCommand("partx", "-d", "--nr", partitionRange, parentPath)
	if err != nil {
		return fmt.Errorf("failed to remove kernel partition entry %d on %s: %w", partition, parentPath, err)
	}

	return nil
}

func (m *OSDManager) cleanupGeneratedAuxDevice(ctx context.Context, kind string, entry *generatedAuxDevice, osdID int64) error {
	if entry == nil {
		return nil
	}

	partitionPath := entry.PartitionPath
	if partitionPath == "" {
		partitionPath = partitionPathFromParentPath(entry.ParentPath, entry.Partition)
	}
	logger.Infof("Cleaning generated %s device for osd.%d: parent=%s partition=%d path=%s encrypted=%t", strings.ToUpper(kind), osdID, entry.ParentPath, entry.Partition, partitionPath, entry.Encrypted)

	if entry.Encrypted {
		logger.Infof("Closing encrypted %s mapper for osd.%d before partition cleanup", strings.ToUpper(kind), osdID)
		if err := m.closeEncryptedAuxDevice(kind, osdID); err != nil {
			return err
		}
	}

	if partitionPath != "" {
		if _, err := m.fs.Stat(partitionPath); err == nil {
			logger.Infof("Wiping generated %s partition %s before delete", strings.ToUpper(kind), partitionPath)
			m.wipeDevice(ctx, partitionPath)
		}
	}

	if err := m.deletePartition(entry.ParentPath, entry.Partition); err != nil {
		return err
	}
	logger.Infof("Cleaned generated %s partition %d on %s for osd.%d", strings.ToUpper(kind), entry.Partition, entry.ParentPath, osdID)
	return nil
}

func (m *OSDManager) cleanupGeneratedAuxEntries(ctx context.Context, manifest *generatedAuxDevicesManifest, osdID int64) error {
	if manifest == nil {
		return nil
	}
	if err := m.cleanupGeneratedAuxDevice(ctx, "wal", manifest.WAL, osdID); err != nil {
		return err
	}
	if err := m.cleanupGeneratedAuxDevice(ctx, "db", manifest.DB, osdID); err != nil {
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
		if err := m.cleanupGeneratedAuxDevice(ctx, "wal", manifest.WAL, osdID); err != nil {
			return err
		}
		manifest.WAL = nil
		if err := m.persistGeneratedAuxManifest(osdDataPath, manifest); err != nil {
			return err
		}
	}

	if manifest.DB != nil {
		if err := m.cleanupGeneratedAuxDevice(ctx, "db", manifest.DB, osdID); err != nil {
			return err
		}
		manifest.DB = nil
		if err := m.persistGeneratedAuxManifest(osdDataPath, manifest); err != nil {
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

	storage, err := m.storage.GetStorage()
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

func (m *OSDManager) createPlannedAuxPartition(plan *plannedAuxPartition) (string, error) {
	if plan == nil {
		return "", nil
	}

	logger.Infof("Creating planned auxiliary partition: %s", plannedAuxPartitionSummary(plan))
	if plan.ResetBeforeUse {
		logger.Infof("Resetting %s carrier %s before repartitioning", strings.ToUpper(plan.Kind), plan.ParentPath)
		if err := m.timeoutWipe(plan.ParentPath); err != nil {
			return "", fmt.Errorf("failed to wipe %s carrier %s: %w", strings.ToUpper(plan.Kind), plan.ParentPath, err)
		}
		m.refreshPartitionTable(plan.ParentPath)
	}
	if plan.ResetBeforeUse || plan.Partition == 1 {
		logger.Infof("Initializing GPT on %s for %s provisioning", plan.ParentPath, strings.ToUpper(plan.Kind))
		if err := m.initializeGPT(plan.ParentPath); err != nil {
			return "", err
		}
	}
	logger.Infof("Creating %s partition %d on %s with size %s", strings.ToUpper(plan.Kind), plan.Partition, plan.ParentPath, formatBytesIEC(int64(plan.SizeBytes)))
	if err := m.createPartition(plan.ParentPath, plan.SizeBytes); err != nil {
		return "", err
	}
	m.refreshPartitionTable(plan.ParentPath)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		path, err := m.resolvePartitionStablePath(plan.ParentPath, plan.Partition)
		if err == nil {
			logger.Infof("Created %s partition %s for parent %s", strings.ToUpper(plan.Kind), path, plan.ParentPath)
			return path, nil
		}
		time.Sleep(1 * time.Second)
	}
	return "", fmt.Errorf("timed out waiting for %s partition %d on %s to appear", plan.Kind, plan.Partition, plan.ParentPath)
}

func buildGeneratedAuxDiskParameter(plan *plannedAuxPartition, path string, encrypt bool, wipe bool) (*types.DiskParameter, *generatedAuxDevice) {
	if plan == nil {
		return nil, nil
	}

	return &types.DiskParameter{Path: path, Encrypt: encrypt, Wipe: wipe, SkipPristineCheck: true}, &generatedAuxDevice{
		ParentPath:    plan.ParentPath,
		Partition:     plan.Partition,
		PartitionPath: path,
		Encrypted:     encrypt,
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
		createdAux := false

		dataStorage, err := m.storage.GetStorage()
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
				if createdAux {
					if cleanupErr := m.cleanupGeneratedAuxEntries(ctx, generatedAux, 0); cleanupErr != nil {
						logger.Warnf("Automatic cleanup after WAL partition creation failure for OSD %s also failed: %v", planned.OSDPath, cleanupErr)
						reportErr = fmt.Sprintf("%s (automatic cleanup also failed: %v)", reportErr, cleanupErr)
						resp.Warnings = append(resp.Warnings, "Automatic cleanup of generated WAL/DB partitions failed; manual cleanup may be required")
					}
				}
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: reportErr})
				break
			}
			createdAux = true
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
				if createdAux {
					if cleanupErr := m.cleanupGeneratedAuxEntries(ctx, generatedAux, 0); cleanupErr != nil {
						logger.Warnf("Automatic cleanup after DB partition creation failure for OSD %s also failed: %v", planned.OSDPath, cleanupErr)
						reportErr = fmt.Sprintf("%s (automatic cleanup also failed: %v)", reportErr, cleanupErr)
						resp.Warnings = append(resp.Warnings, "Automatic cleanup of generated WAL/DB partitions failed; manual cleanup may be required")
					}
				}
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: reportErr})
				break
			}
			createdAux = true
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
