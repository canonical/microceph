package ceph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const smbLocalSAMFence = "local-sam-v1\n"

func smbLocalSAMFile(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("SMB local SAM path is unsafe; explicit recovery is required")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("SMB local SAM is missing; explicit recovery is required: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() == 0 {
		return fmt.Errorf("SMB local SAM file is unsafe or empty; explicit recovery is required")
	}
	return nil
}

func checkSMBLocalSAM(root string) error {
	fence := filepath.Join(root, "identity/local-sam")
	err := smbLocalSAMFile(fence)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(fence)
	if err != nil {
		return err
	}
	if string(raw) != smbLocalSAMFence {
		return fmt.Errorf("SMB local SAM fence is unknown; explicit recovery is required")
	}
	for _, name := range []string{"passdb.tdb", "secrets.tdb"} {
		err = smbLocalSAMFile(filepath.Join(root, "samba/private", name))
		if err != nil {
			return err
		}
	}
	return nil
}

func checkSMBFreshLocalSAM(root string, beforePublication bool) error {
	paths := []string{"identity/local-sam", "identity/local-sam.tmp"}
	if beforePublication {
		paths = append(paths, "config/context.json")
	}
	for _, name := range paths {
		_, err := os.Lstat(filepath.Join(root, name))
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("SMB local SAM has contradictory retained evidence; explicit recovery is required")
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "samba/private"))
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("SMB local SAM is not demonstrably fresh; explicit recovery is required")
	}
	return nil
}
