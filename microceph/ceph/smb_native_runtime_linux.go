package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/common"
)

func prepareSMBAccounts(ctx context.Context, root string) error {
	// This work previously ran inside smbd's 30-second startup probe window.
	// Moving it before snapd Start must not turn it into an unbounded request.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := ctx.Err()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
	if err != nil {
		return err
	}
	var candidate smbCandidate
	err = json.Unmarshal(raw, &candidate)
	if err != nil {
		return err
	}
	err = smbNativeRoot(root, candidate.Spec.ClusterID)
	if err != nil {
		return err
	}
	if candidate.Spec.hasFeature("domain") {
		return fmt.Errorf("AD member must not import local SAM accounts")
	}
	document, err := smbCombinedDocument(candidate)
	if err != nil {
		return err
	}
	request := smbBindingRequest{ClusterID: candidate.Spec.ClusterID, Config: document, ConfigPath: filepath.Join(root, "config/smb.conf")}
	err = smbPythonBinding(ctx, root)("accounts", request, nil)
	if err != nil {
		return err
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	return setSMBPrepared(root, "smbd", false)
}

func prepareSMBCTDBFiles(ctx context.Context, root, snap string) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	// Old helper endpoints carry no authority after cutover. Remove only
	// known owner-local socket/PID leaves, never retained upstream databases.
	for _, name := range []string{"ctdb/run/native-ready.socket", "ctdb/run/ctdb-nodes.pid", "samba/run/native-ad.socket"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("unexpected legacy SMB endpoint type")
		}
		err = os.Remove(path)
		if err != nil {
			return err
		}
	}
	for _, helper := range []string{"ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"} {
		err = ctx.Err()
		if err != nil {
			return err
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
	directory := filepath.Join(root, "ctdb/events/legacy")
	err = smbPrivateDir(directory)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "00.ctdb.script" {
			return fmt.Errorf("unexpected enabled CTDB event script")
		}
	}
	for _, name := range []string{"ctdb/run", "ctdb/scripts"} {
		err = smbPrivateDir(filepath.Join(root, name))
		if err != nil {
			return err
		}
	}
	for _, name := range names {
		err = ctx.Err()
		if err != nil {
			return err
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
	return nil
}

// Resource aliases resolve through the existing recipe layout in each app's
// namespace, never through a snap revision that can later be garbage-collected.
func smbCTDBResourcesPrepared(root string) bool {
	for _, name := range []string{"functions", "notify.sh", "events/legacy/00.ctdb.script"} {
		target, err := os.Readlink(filepath.Join(root, "ctdb", name))
		if err != nil || target != filepath.Join("/usr/share/ctdb", name) {
			return false
		}
	}
	return true
}

// The layout source directory is never renamed: snapd binds its inode. Only
// children are published atomically, after consumers have been quiesced.
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
	if configOnly {
		current, readErr := os.ReadFile(filepath.Join(directory, "ctdb.conf"))
		owner, ownerErr := os.ReadFile(filepath.Join(directory, "owner"))
		if (ownerErr == nil && string(owner) != filepath.Base(root)) || (readErr == nil && !bytes.Equal(current, data)) {
			return fmt.Errorf("CTDB layout belongs to another owner or configuration")
		}
		if readErr == nil && ownerErr == nil {
			if string(owner) != filepath.Base(root) || !bytes.Equal(current, data) {
				return fmt.Errorf("CTDB layout changed; explicit maintenance required")
			}
			return nil
		}
		if (!os.IsNotExist(readErr) && readErr != nil) || (!os.IsNotExist(ownerErr) && ownerErr != nil) {
			return fmt.Errorf("CTDB layout cannot be inspected")
		}
		active, err := activeSMBRoot(filepath.Dir(root))
		if err != nil || active != root {
			return fmt.Errorf("CTDB layout owner changed")
		}
	}
	err = writeSMBNativeFile(filepath.Join(directory, "ctdb.conf"), data)
	if err != nil {
		return err
	}
	return writeSMBNativeFile(filepath.Join(directory, "owner"), []byte(filepath.Base(root)))
}

func loadSMBLaunchForPublication(root string) (smbLaunch, error) {
	var launch smbLaunch
	data, err := os.ReadFile(filepath.Join(root, "config/context.json"))
	if err != nil {
		return launch, err
	}
	err = json.Unmarshal(data, &launch)
	if err == nil && launch.AD != nil && !launch.Clustered {
		err = fmt.Errorf("AD member requires clustered runtime context")
	}
	if err == nil && launch.Clustered && launch.CTDBPNN == nil {
		err = fmt.Errorf("CTDB context lacks assigned PNN")
	}
	return launch, err
}

func loadSMBLaunch(root string) (smbLaunch, error) {
	err := checkSMBStartIntent(root)
	if err != nil {
		return smbLaunch{}, err
	}
	return loadSMBLaunchForPublication(root)
}

func activeSMBRoot(base string) (string, error) {
	data, err := os.ReadFile(filepath.Join(base, "owner.json"))
	if err != nil {
		return "", err
	}
	id := string(data)
	err = validateSMBClusterID(id)
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, id)
	err = smbNativeRoot(root, id)
	return root, err
}

func checkSMBCTDBOwner(root string) error {
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil {
		return err
	}
	if active != root {
		return fmt.Errorf("CTDB readiness owner mismatch")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if !launch.Clustered {
		return fmt.Errorf("CTDB readiness requires prepared clustered context")
	}
	return nil
}

func smbReadyNative(ctx context.Context, root, role string) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD != nil && (role == "smbd" || role == "winbindd") {
		err = smbADReadyFunc(ctx, root)
		if err != nil || role == "winbindd" {
			return err
		}
	}
	switch role {
	case "ctdbd":
		return checkSMBCTDBHealth(ctx, root, true)
	case "cephfs-proxy":
		readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(readyCtx, "unix", filepath.Join(root, "proxy-work/libcephfsd.sock"))
		if err != nil {
			return err
		}
		return conn.Close()
	case "smbd":
		data, err := os.ReadFile(filepath.Join(root, "samba/run/smbd.pid"))
		if err != nil {
			return err
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return fmt.Errorf("invalid Samba PID")
		}
		err = unix.Kill(pid, 0)
		if err != nil {
			return err
		}
		readyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err = common.ProcessExec.RunCommandContext(readyCtx, filepath.Join(os.Getenv("SNAP"), "bin/smbcontrol"), "--configfile", filepath.Join(root, "config/smb.conf"), strconv.Itoa(pid), "ping")
		return err
	case "smbmetrics":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/metrics", launch.MetricsPort), nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("metrics not ready")
		}
		var first [1]byte
		_, err = io.ReadFull(response.Body, first[:])
		return err
	default:
		return fmt.Errorf("native readiness role is not prepared")
	}
}
