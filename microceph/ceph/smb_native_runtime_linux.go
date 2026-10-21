package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/common"
)

// RunSMBRuntime runs only fixed snap app roles and restricted CTDB controls.
// Configuration preparation/publication and daemon-side readiness are not app
// operations. Namespace entry must never be called by the MicroCeph daemon.
func RunSMBRuntime(ctx context.Context, role, base string, args ...string) (retErr error) {
	if (role != "ctdb-control" && len(args) != 0) || (role == "ctdb-control" && len(args) == 0) {
		return fmt.Errorf("unexpected SMB runtime arguments")
	}
	// Validate the fixed role before looking up private cluster state.
	switch role {
	case "smbd", "smbmetrics", "ctdbd", "ctdb-nodes", "cephfs-proxy", "winbindd", "smb-remote", "ctdb-control":
	default:
		return fmt.Errorf("unsupported SMB runtime role")
	}
	root, err := activeSMBRoot(base)
	if err != nil {
		return fmt.Errorf("native SMB owner is unavailable or invalid")
	}
	defer smbNativeFailure(root, &retErr)
	if role == "ctdb-control" {
		return controlSMBNative(ctx, root, args)
	}
	return launchSMBNative(ctx, root, role, nativeSMBRuntimeOps(ctx, root, role))
}

type smbNativeMount struct {
	Source string
	Target string
	Flags  uintptr
}
type smbRolePlan struct {
	Args      []string
	Mounts    []smbNativeMount
	Env       map[string]string
	Directory string
}

type smbRuntimeOps struct {
	enter     func(smbRolePlan) error
	ctdbReady func(context.Context, string) error
	accounts  func(context.Context, string) error
	member    func(context.Context, string, bool) error
	exec      func(smbRolePlan) error
}

func launchSMBNative(ctx context.Context, root, role string, ops smbRuntimeOps) error {
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil {
		return err
	}
	if active != root {
		return fmt.Errorf("native launch owner mismatch")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	plan, err := smbNativeRolePlan(role, root, launch, os.Getenv("SNAP"))
	if err != nil {
		return err
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	err = ops.enter(plan)
	if err != nil {
		return err
	}
	if (role == "smbd" || role == "winbindd") && launch.Clustered {
		err = ops.ctdbReady(ctx, root)
		if err != nil {
			return err
		}
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	if launch.AD != nil && role == "smbd" {
		err = ops.member(ctx, root, false)
		if err != nil {
			return err
		}
	}
	if role == "smbd" && launch.AD == nil {
		err = ops.accounts(ctx, root)
		if err != nil {
			return err
		}
	}
	err = checkSMBStartIntent(root)
	if err != nil {
		return err
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	return ops.exec(plan)
}

// enterSMBNativeContext is used only by the standalone fixed-app helper, never
// by the running MicroCeph daemon. Keep the goroutine on this private-namespace
// thread until exec or process exit; unlocking could affect another goroutine.
func enterSMBNativeContext(plan smbRolePlan) error {
	runtime.LockOSThread()
	err := unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		return fmt.Errorf("native mount namespace: %w", err)
	}
	for _, mount := range plan.Mounts {
		if mount.Target == "/run/samba/winbindd" {
			err = prepareSMBWinbindMountpoint(mount.Target, uint32(os.Geteuid()))
			if err != nil {
				return err
			}
		}
		if mount.Target == "/run/ctdb" {
			err = prepareSMBCTDBMountpoint(mount.Target, uint32(os.Geteuid()))
			if err != nil {
				return err
			}
		}
		err = unix.Mount(mount.Source, mount.Target, "", mount.Flags, "")
		if err != nil {
			return fmt.Errorf("native private bind: %w", err)
		}
	}
	for key, value := range plan.Env {
		err = os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

func execSMBNativeRole(root, role string, plan smbRolePlan) error {
	if role == "ctdbd" {
		// The pinned foreground CTDB binary logs to stderr. Keep it private.
		fd, err := unix.Open(filepath.Join(root, "diagnostics/ctdbd.log"), unix.O_WRONLY|unix.O_CREAT|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		err = unix.Dup2(fd, 1)
		if err != nil {
			return err
		}
		err = unix.Dup2(fd, 2)
		if err != nil {
			return err
		}
	}
	if plan.Directory != "" {
		err := os.Chdir(plan.Directory)
		if err != nil {
			return err
		}
	}
	return unix.Exec(plan.Args[0], plan.Args, os.Environ())
}

func nativeSMBRuntimeOps(ctx context.Context, root, role string) smbRuntimeOps {
	return smbRuntimeOps{
		enter:     enterSMBNativeContext,
		ctdbReady: func(ctx context.Context, root string) error { return checkSMBCTDBHealth(ctx, root, false) },
		accounts:  prepareSMBAccounts,
		member: func(ctx context.Context, root string, initial bool) error {
			err := checkSMBADMember(ctx, root, initial)
			if err != nil {
				return err
			}
			return smbADControl(ctx, root, "ready")
		},
		exec: func(plan smbRolePlan) error {
			if role == "winbindd" {
				return serveSMBADRuntime(ctx, root, plan)
			}
			if role == "ctdb-nodes" {
				return monitorSMBCTDB(ctx, root)
			}
			return execSMBNativeRole(root, role, plan)
		},
	}
}

func prepareSMBAccounts(ctx context.Context, root string) error {
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
	return smbPythonBinding(ctx, root)("accounts", request, nil)
}

func controlSMBNative(ctx context.Context, root string, args []string) error {
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	plan, err := smbNativeRolePlan("ctdb-control", root, launch, os.Getenv("SNAP"))
	if err != nil {
		return err
	}
	err = enterSMBNativeContext(plan)
	if err != nil {
		return err
	}
	if len(args) == 2 && args[0] == "ready" && (args[1] == "ctdbd" || args[1] == "ctdb-nodes") {
		return checkSMBCTDBHealth(ctx, root, args[1] == "ctdb-nodes")
	}
	output, err := runSMBCTDB(ctx, args[0], args[1:]...)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, output)
	return err
}

func smbNativeRolePlan(role, root string, launch smbLaunch, snap string) (smbRolePlan, error) {
	if launch.AD != nil && !launch.Clustered {
		return smbRolePlan{}, fmt.Errorf("AD member requires clustered runtime context")
	}
	plan := smbRolePlan{Mounts: []smbNativeMount{{Target: "/", Flags: unix.MS_REC | unix.MS_SLAVE}}, Env: map[string]string{
		"LIBCEPHFSD_SOCKET":    root + "/proxy-work/libcephfsd.sock",
		"SMBMETRICS_SMBSTATUS": snap + "/bin/smbstatus",
		"SMBMETRICS_SMBCONF":   root + "/config/smb.conf",
		"SMBMETRICS_TESTPARM":  snap + "/bin/testparm",
		"SMBMETRICS_CTDB":      snap + "/bin/ctdb",
	}}
	switch role {
	case "smbd":
		plan.Args = []string{snap + "/bin/smbd", "--foreground", "--no-process-group", "--configfile", root + "/config/smb.conf"}
		for _, name := range []string{"passwd", "group", "nsswitch.conf"} {
			plan.Mounts = append(plan.Mounts, smbNativeMount{Source: root + "/config/" + name, Target: "/etc/" + name, Flags: unix.MS_BIND})
		}
	case "winbindd":
		if launch.AD == nil || !launch.Clustered {
			return plan, fmt.Errorf("Winbind requires prepared clustered AD member context")
		}
		plan.Args = []string{snap + "/bin/winbindd", "--foreground", "--no-process-group", "--configfile", root + "/config/smb.conf"}
	case "smbmetrics":
		plan.Args = []string{snap + "/bin/smbmetrics", "--address", "0.0.0.0", "--port", strconv.Itoa(launch.MetricsPort)}
	case "ctdbd":
		plan.Args = []string{snap + "/bin/ctdbd", "--interactive"}
	case "ctdb-nodes":
		// The monitor runs in this app process after entering its namespace.
	case "ctdb-control":
		plan.Args = []string{snap + "/bin/ctdb"}
	case "cephfs-proxy":
		plan.Args = []string{snap + "/bin/libcephfsd", "--socket", root + "/proxy-work/libcephfsd.sock"}
		plan.Directory = root + "/proxy-work"
	default:
		return plan, fmt.Errorf("native role is not prepared")
	}
	if launch.AD != nil && (role == "smbd" || role == "winbindd") {
		plan.Mounts = append(plan.Mounts, smbNativeMount{Source: root + "/samba/winbindd", Target: "/run/samba/winbindd", Flags: unix.MS_BIND})
		if role == "winbindd" {
			for _, name := range []string{"passwd", "group", "nsswitch.conf"} {
				plan.Mounts = append(plan.Mounts, smbNativeMount{Source: root + "/config/" + name, Target: "/etc/" + name, Flags: unix.MS_BIND})
			}
		}
	}
	ctdbRole := role == "ctdbd" || role == "ctdb-nodes" || role == "ctdb-control"
	if ctdbRole && !launch.Clustered {
		return plan, fmt.Errorf("CTDB role requires prepared clustered context")
	}
	if ctdbRole || ((role == "smbd" || role == "winbindd") && launch.Clustered) {
		for _, pair := range [][2]string{{"ctdb", "/etc/ctdb"}, {"ctdb/run", "/run/ctdb"}, {"ctdb", "/var/lib/ctdb"}} {
			plan.Mounts = append(plan.Mounts, smbNativeMount{Source: root + "/" + pair[0], Target: pair[1], Flags: unix.MS_BIND})
		}
	}
	if launch.CustomDNS {
		plan.Mounts = append(plan.Mounts, smbNativeMount{Source: root + "/config/resolv.conf", Target: "/etc/resolv.conf", Flags: unix.MS_BIND})
	}
	if launch.Clustered {
		plan.Env["CTDB_BASE"] = root + "/ctdb"
		plan.Env["CTDB"] = snap + "/bin/ctdb"
	}
	return plan, nil
}

func prepareSMBCTDBMountpoint(path string, owner uint32) error {
	err := os.Mkdir(path, 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != owner || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe CTDB runtime mountpoint")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("CTDB runtime mountpoint contains unrelated state")
	}
	return nil
}

func prepareSMBCTDBFiles(ctx context.Context, root, snap string) (retErr error) {
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	err = ctx.Err()
	if err != nil {
		return err
	}
	for _, helper := range []string{"ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"} {
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
	err = os.Remove(filepath.Join(root, "ctdb/run/ctdb-nodes.pid"))
	if err != nil && !os.IsNotExist(err) {
		return err
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
		err = os.Symlink(filepath.Join(snap, "share/ctdb", name), target)
		if err != nil {
			return err
		}
	}
	return nil
}

func loadSMBLaunch(root string) (smbLaunch, error) {
	var launch smbLaunch
	err := checkSMBStartIntent(root)
	if err != nil {
		return launch, err
	}
	data, err := os.ReadFile(filepath.Join(root, "config/context.json"))
	if err != nil {
		return launch, err
	}
	err = json.Unmarshal(data, &launch)
	if err != nil {
		return launch, err
	}
	if launch.Clustered && launch.CTDBPNN == nil {
		return launch, fmt.Errorf("CTDB context lacks assigned PNN")
	}
	return launch, nil
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

func probeSMBCTDB(ctx context.Context, root string) error {
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "unix", filepath.Join(root, "ctdb/run/native-ready.socket"))
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(probeCtx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := probeCtx.Deadline()
	err = conn.SetDeadline(deadline)
	if err != nil {
		return err
	}
	var response [1]byte
	_, err = io.ReadFull(conn, response[:])
	if err != nil {
		return err
	}
	if response[0] != '1' {
		return fmt.Errorf("CTDB monitor did not confirm readiness")
	}
	err = probeCtx.Err()
	if err != nil {
		return err
	}
	return checkSMBCTDBOwner(root)
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
		err = smbADControlFunc(ctx, root, "ready")
		if err != nil {
			return err
		}
		if role == "winbindd" {
			return nil
		}
	}
	switch role {
	case "ctdbd", "ctdb-nodes":
		return probeSMBCTDB(ctx, root)
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
		if err != nil {
			return err
		}
		if pid <= 0 {
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

// The NSS compiled path needs a containing /run/samba directory on hosts without
// Samba. Never overlay host Samba state, accept symlinks, or export privileged
// pipes; only an empty public Winbind mountpoint may exist under this parent.
func prepareSMBWinbindMountpoint(path string, owner uint32) error {
	parent := filepath.Dir(path)
	err := os.Mkdir(parent, 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != owner || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe Winbind runtime parent")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			return fmt.Errorf("Winbind runtime parent contains unrelated state")
		}
	}
	return prepareSMBCTDBMountpoint(path, owner)
}
