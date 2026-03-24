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
	"github.com/spf13/afero"
)

type plannedAuxPartition struct {
	Kind       string
	ParentPath string
	Partition  uint64
	SizeBytes  uint64
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

func planAuxiliaryPartitionsDetailed(osdPaths []string, carriers []api.ResourcesStorageDisk, sizeBytes uint64, kind string) ([]*plannedAuxPartition, error) {
	if len(osdPaths) == 0 || len(carriers) == 0 {
		return make([]*plannedAuxPartition, len(osdPaths)), nil
	}

	states := make([]plannedCarrierState, len(carriers))
	for i, disk := range carriers {
		used := diskUsedBytes(disk)
		remaining := uint64(0)
		if disk.Size > used {
			remaining = disk.Size - used
		}
		states[i] = plannedCarrierState{
			Path:          dsl.GetDevicePath(disk),
			Disk:          disk,
			PartitionNo:   nextPartitionNumber(disk),
			PartitionCnt:  len(disk.Partitions),
			RemainingSize: remaining,
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
			Kind:       kind,
			ParentPath: states[choice].Path,
			Partition:  states[choice].PartitionNo,
			SizeBytes:  sizeBytes,
		}
		states[choice].RemainingSize -= sizeBytes
		states[choice].PartitionCnt++
		states[choice].PartitionNo++
	}

	return out, nil
}

func (m *OSDManager) buildDSLProvisionPlan(ctx context.Context, req types.DisksPost) (*dslProvisionPlan, error) {
	osdResult, err := m.matchOSDDisksWithDSL(ctx, req.OSDMatch)
	if err != nil {
		return nil, err
	}
	if osdResult.ValidationError != "" {
		return nil, fmt.Errorf(osdResult.ValidationError)
	}

	plan := &dslProvisionPlan{}
	if len(osdResult.MatchedDisks) == 0 {
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
	osdPathSet := buildPathSet(osdResult.MatchedDisks)

	var walMatches, dbMatches []api.ResourcesStorageDisk
	if req.WALMatch != "" {
		walMatches, err = m.matchAuxiliaryDisksWithDSL(ctx, req.WALMatch, osdPathSet)
		if err != nil {
			return nil, err
		}
		if len(walMatches) == 0 {
			plan.Warnings = append(plan.Warnings, "WAL match expression resolved to no devices; proceeding without WAL")
		}
	}
	if req.DBMatch != "" {
		dbMatches, err = m.matchAuxiliaryDisksWithDSL(ctx, req.DBMatch, osdPathSet)
		if err != nil {
			return nil, err
		}
		if len(dbMatches) == 0 {
			plan.Warnings = append(plan.Warnings, "DB match expression resolved to no devices; proceeding without DB")
		}
	}

	walPathSet := buildPathSet(walMatches)
	dbPathSet := buildPathSet(dbMatches)
	if overlap := intersectPathSets(osdPathSet, walPathSet); len(overlap) > 0 {
		return nil, fmt.Errorf("OSD and WAL match sets overlap: %s", strings.Join(overlap, ", "))
	}
	if overlap := intersectPathSets(osdPathSet, dbPathSet); len(overlap) > 0 {
		return nil, fmt.Errorf("OSD and DB match sets overlap: %s", strings.Join(overlap, ", "))
	}
	if overlap := intersectPathSets(walPathSet, dbPathSet); len(overlap) > 0 {
		return nil, fmt.Errorf("WAL and DB match sets overlap: %s", strings.Join(overlap, ", "))
	}

	if req.WALMatch != "" && len(walMatches) > 0 {
		sizeBytes, err := units.ParseByteSizeString(req.WALSize)
		if err != nil {
			return nil, fmt.Errorf("invalid WAL size: %v", err)
		}
		walPlan, err := planAuxiliaryPartitionsDetailed(osdPaths, walMatches, uint64(sizeBytes), "wal")
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
		dbPlan, err := planAuxiliaryPartitionsDetailed(osdPaths, dbMatches, uint64(sizeBytes), "db")
		if err != nil {
			return nil, err
		}
		for i := range plan.OSDs {
			plan.OSDs[i].DB = dbPlan[i]
		}
	}

	return plan, nil
}

func dryRunResponseFromProvisionPlan(plan *dslProvisionPlan) types.DiskAddResponse {
	resp := types.DiskAddResponse{Warnings: append([]string(nil), plan.Warnings...)}
	resp.DryRunPlan = make([]types.DryRunOSDPlan, len(plan.OSDs))
	for i, osd := range plan.OSDs {
		resp.DryRunPlan[i].OSDPath = osd.OSDPath
		if osd.WAL != nil {
			resp.DryRunPlan[i].WAL = &types.DryRunPartitionPlan{
				Kind:       osd.WAL.Kind,
				ParentPath: osd.WAL.ParentPath,
				Partition:  osd.WAL.Partition,
				Size:       formatBytesIEC(int64(osd.WAL.SizeBytes)),
			}
		}
		if osd.DB != nil {
			resp.DryRunPlan[i].DB = &types.DryRunPartitionPlan{
				Kind:       osd.DB.Kind,
				ParentPath: osd.DB.ParentPath,
				Partition:  osd.DB.Partition,
				Size:       formatBytesIEC(int64(osd.DB.SizeBytes)),
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
	cmd := fmt.Sprintf("printf ',+%dMiB\n' | sfdisk --append %q", sizeMiB, parentPath)
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
		for _, symlinkName := range []string{"block", "block.wal", "block.db"} {
			targetPath := filepath.Join(baseDir, osdDir.Name(), symlinkName)
			resolved := resolvePathBestEffort(targetPath)
			parent, ok := lookup[resolved]
			if !ok {
				continue
			}
			entry := usage[parent]
			if symlinkName == "block" {
				entry.HasData = true
			} else {
				entry.HasAux = true
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

	if err := afero.WriteFile(m.fs, generatedAuxManifestPath(osdDataPath), data, 0600); err != nil {
		return fmt.Errorf("failed to write generated aux manifest: %w", err)
	}

	return nil
}

func (m *OSDManager) readGeneratedAuxManifest(osdDataPath string) (*generatedAuxDevicesManifest, error) {
	data, err := afero.ReadFile(m.fs, generatedAuxManifestPath(osdDataPath))
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
	_, err := m.runner.RunCommand("sfdisk", "--delete", parentPath, strconv.FormatUint(partition, 10))
	if err != nil {
		return fmt.Errorf("failed to delete partition %d on %s: %w", partition, parentPath, err)
	}
	return nil
}

func (m *OSDManager) cleanupGeneratedAuxDevice(ctx context.Context, kind string, entry *generatedAuxDevice, osdID int64) error {
	if entry == nil {
		return nil
	}

	if entry.Encrypted {
		if err := m.closeEncryptedAuxDevice(kind, osdID); err != nil {
			return err
		}
	}

	partitionPath := entry.PartitionPath
	if partitionPath == "" {
		partitionPath = partitionPathFromParentPath(entry.ParentPath, entry.Partition)
	}

	if partitionPath != "" {
		if _, err := m.fs.Stat(partitionPath); err == nil {
			m.wipeDevice(ctx, partitionPath)
		}
	}

	if err := m.deletePartition(entry.ParentPath, entry.Partition); err != nil {
		return err
	}
	m.refreshPartitionTable(entry.ParentPath)
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

	if err := m.cleanupGeneratedAuxDevice(ctx, "wal", manifest.WAL, osdID); err != nil {
		return err
	}
	if err := m.cleanupGeneratedAuxDevice(ctx, "db", manifest.DB, osdID); err != nil {
		return err
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
	if plan.Partition == 1 {
		if err := m.initializeGPT(plan.ParentPath); err != nil {
			return "", err
		}
	}
	if err := m.createPartition(plan.ParentPath, plan.SizeBytes); err != nil {
		return "", err
	}
	m.refreshPartitionTable(plan.ParentPath)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		path, err := m.resolvePartitionStablePath(plan.ParentPath, plan.Partition)
		if err == nil {
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

	return &types.DiskParameter{Path: path, Encrypt: encrypt, Wipe: wipe}, &generatedAuxDevice{
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

	for _, planned := range plan.OSDs {
		var walParam *types.DiskParameter
		var dbParam *types.DiskParameter
		var generatedAux *generatedAuxDevicesManifest
		createdAux := false

		dataStorage, err := m.storage.GetStorage()
		if err != nil {
			resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: fmt.Sprintf("unable to list system disks: %v", err)})
			break
		}

		if planned.WAL != nil {
			path, err := createPlannedAuxPartitionFn(m, planned.WAL)
			if err != nil {
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: err.Error()})
				resp.Warnings = append(resp.Warnings, "Partial failure occurred; generated WAL/DB partitions may need manual cleanup")
				break
			}
			createdAux = true
			var generatedWAL *generatedAuxDevice
			walParam, generatedWAL = buildGeneratedAuxDiskParameter(planned.WAL, path, req.WALEncrypt, req.WALWipe)
			if generatedAux == nil {
				generatedAux = &generatedAuxDevicesManifest{}
			}
			generatedAux.WAL = generatedWAL
		}

		if planned.DB != nil {
			path, err := createPlannedAuxPartitionFn(m, planned.DB)
			if err != nil {
				resp.Reports = append(resp.Reports, types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: err.Error()})
				resp.Warnings = append(resp.Warnings, "Partial failure occurred; generated WAL/DB partitions may need manual cleanup")
				break
			}
			createdAux = true
			var generatedDB *generatedAuxDevice
			dbParam, generatedDB = buildGeneratedAuxDiskParameter(planned.DB, path, req.DBEncrypt, req.DBWipe)
			if generatedAux == nil {
				generatedAux = &generatedAuxDevicesManifest{}
			}
			generatedAux.DB = generatedDB
		}

		err = doAddOSDWithStorageFn(m, ctx, types.DiskParameter{Path: planned.OSDPath, Encrypt: req.Encrypt, Wipe: req.Wipe}, walParam, dbParam, dataStorage, generatedAux)
		if err != nil {
			report := types.DiskAddReport{Path: planned.OSDPath, Report: "Failure", Error: err.Error()}
			resp.Reports = append(resp.Reports, report)
			if createdAux {
				resp.Warnings = append(resp.Warnings, "Partial failure occurred; generated WAL/DB partitions may need manual cleanup")
			}
			break
		}

		report := types.DiskAddReport{Path: planned.OSDPath, Report: "Success", Error: ""}
		resp.Reports = append(resp.Reports, report)
	}

	return resp
}
