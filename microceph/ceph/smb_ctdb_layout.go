package ceph

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func checkSMBCTDBOwner(root string) error {
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil || active != root {
		return fmt.Errorf("CTDB owner is unavailable or changed")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if !launch.Clustered {
		return fmt.Errorf("CTDB requires clustered context")
	}
	return nil
}

func prepareSMBCTDBFiles(ctx context.Context, root, snap string) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	for _, helper := range []string{"ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := os.Stat(filepath.Join(snap, "libexec/ctdb", helper))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("required CTDB helper is not executable")
		}
	}
	names := []string{"functions", "notify.sh", "events/legacy/00.ctdb.script"}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(snap, "share/ctdb", name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required CTDB event resource is absent")
		}
	}
	for _, name := range []string{"ctdb/events/legacy", "ctdb/run", "ctdb/scripts"} {
		err = smbPrivateDir(filepath.Join(root, name))
		if err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "ctdb/events/legacy"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "00.ctdb.script" {
			return fmt.Errorf("unexpected enabled CTDB event script")
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target := filepath.Join(root, "ctdb", name)
		err = os.Remove(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		err = os.Symlink(filepath.Join("/usr/share/ctdb", name), target)
		if err != nil {
			return err
		}
	}
	if !smbCTDBResourcesPrepared(root) {
		return fmt.Errorf("CTDB resource aliases are not confirmed")
	}
	return nil
}

func smbCTDBResourcesPrepared(root string) bool {
	for _, name := range []string{"functions", "notify.sh", "events/legacy/00.ctdb.script"} {
		target, err := os.Readlink(filepath.Join(root, "ctdb", name))
		if err != nil || target != filepath.Join("/usr/share/ctdb", name) {
			return false
		}
	}
	return true
}

// The directory inode is a snap layout source and must never be replaced.
func publishSMBCTDBLayout(root string, configOnly bool) error {
	launch, err := loadSMBLaunchForPublication(root)
	if err != nil || !launch.Clustered {
		return err
	}
	directory := filepath.Join(filepath.Dir(root), ".conf/ctdb")
	err = smbPrivateDir(directory)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "ctdb/ctdb.conf"))
	if err != nil {
		return err
	}
	for _, kind := range []string{"volatile", "persistent", "state"} {
		if !strings.Contains(string(data), kind+" database directory = "+root+"/ctdb/"+kind+"\n") {
			return fmt.Errorf("CTDB database directory is not owner-local")
		}
	}
	resources := []string{"events", "functions", "notify.sh"}
	if configOnly {
		current, readErr := os.ReadFile(filepath.Join(directory, "ctdb.conf"))
		owner, ownerErr := os.ReadFile(filepath.Join(directory, "owner"))
		if readErr != nil || ownerErr != nil || string(owner) != filepath.Base(root) || !bytes.Equal(current, data) {
			return fmt.Errorf("CTDB layout changed; explicit maintenance required")
		}
		for _, name := range resources {
			target, err := os.Readlink(filepath.Join(directory, name))
			if err != nil || target != filepath.Join(root, "ctdb", name) {
				return fmt.Errorf("CTDB layout resources changed")
			}
		}
		return nil
	}
	for _, name := range resources {
		path := filepath.Join(directory, name)
		target := filepath.Join(root, "ctdb", name)
		current, err := os.Readlink(path)
		if err == nil && current == target {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("unsafe CTDB layout resource: %w", err)
		}
		tmp := path + ".tmp"
		err = os.Symlink(target, tmp)
		if err != nil {
			return err
		}
		err = os.Rename(tmp, path)
		if err != nil {
			os.Remove(tmp)
			return err
		}
	}
	err = writeSMBNativeFile(filepath.Join(directory, "ctdb.conf"), data)
	if err != nil {
		return err
	}
	return writeSMBNativeFile(filepath.Join(directory, "owner"), []byte(filepath.Base(root)))
}
