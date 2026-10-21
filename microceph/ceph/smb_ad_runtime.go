package ceph

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const smbADJoinTimeout = 60 * time.Second

// Only the fixed Winbind app enters these namespaces. The daemon consumes a
// committed SQL attempt before invoking join; ordinary app startup never joins.
var smbADControlFunc = smbADControl

func smbADControl(ctx context.Context, root, operation string) error {
	code, ok := map[string]byte{"join": 'J', "ready": 'R', "start": 'S', "probe": 'P'}[operation]
	if !ok {
		return fmt.Errorf("unsupported AD operation")
	}
	budget := 15 * time.Second
	if operation == "join" {
		budget = smbADJoinTimeout + 5*time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(root, "samba/run/native-ad.socket"))
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	err = conn.SetDeadline(deadline)
	if err != nil {
		return err
	}
	_, err = conn.Write([]byte{code})
	if err != nil {
		return err
	}
	var response [1]byte
	_, err = io.ReadFull(conn, response[:])
	if err != nil {
		return err
	}
	if response[0] != '1' {
		return fmt.Errorf("AD member operation is not confirmed; inspect private diagnostics; never automatically retry joining")
	}
	return ctx.Err()
}

// Requests are fixed bytes on a root-only socket; no paths, credentials or
// arbitrary commands cross it. Losing the daemon cancels an in-flight join.
func serveSMBADControls(ctx context.Context, listener *net.UnixListener, operation func(context.Context, byte) error) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for ctx.Err() == nil {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		closeOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
		err = conn.SetDeadline(time.Now().Add(smbADJoinTimeout + time.Second))
		var request [1]byte
		if err == nil {
			_, err = io.ReadFull(conn, request[:])
		}
		if err != nil {
			_ = conn.Close()
			closeOnCancel()
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, smbADJoinTimeout)
		disconnected := make(chan struct{})
		go func() { var extra [1]byte; _, _ = conn.Read(extra[:]); cancel(); close(disconnected) }()
		err = operation(requestCtx, request[0])
		response := byte('0')
		if err == nil && requestCtx.Err() == nil {
			response = '1'
		}
		_, _ = conn.Write([]byte{response})
		_ = conn.Close()
		closeOnCancel()
		cancel()
		<-disconnected
	}
	return nil
}

func serveSMBADRuntime(ctx context.Context, root string, plan smbRolePlan) (retErr error) {
	parentCtx := ctx
	listener, guard, err := openSMBPrivateListener(filepath.Join(root, "samba/run/native-ad.socket"))
	if err != nil {
		return err
	}
	defer guard.Close()
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var child *exec.Cmd
	var exited chan error
	start := func(ctx context.Context) error {
		if child != nil {
			return nil
		}
		err := checkSMBADMember(ctx, root, true)
		if err != nil {
			return err
		}
		// The long-lived child must follow the app lifetime, not this request.
		child = exec.CommandContext(context.WithoutCancel(ctx), plan.Args[0], plan.Args[1:]...)
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
		// Samba's own logging is private. Never inherit the journal for diagnostics.
		log, err := os.OpenFile(filepath.Join(root, "diagnostics/winbindd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			child = nil
			return err
		}
		child.Stdout = log
		child.Stderr = log
		err = child.Start()
		_ = log.Close()
		if err != nil {
			child = nil
			return err
		}
		exited = make(chan error, 1)
		go func(cmd *exec.Cmd) { exited <- cmd.Wait(); cancel() }(child)
		return nil
	}
	defer func() {
		if child != nil {
			select {
			case <-exited:
			default:
				_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
				<-exited
			}
			if parentCtx.Err() == nil {
				retErr = fmt.Errorf("Winbind child exited; member service stopped")
			}
		}
	}()
	// Reboots/restarts use retained trust only. Missing state waits for explicit
	// SQL-authorized initialization; it can never cause an automatic join.
	_ = start(ctx)
	attempted := false
	return serveSMBADControls(ctx, listener, func(requestCtx context.Context, op byte) (err error) {
		defer func() {
			if err != nil {
				_ = writeSMBNativeFile(filepath.Join(root, "diagnostics/ad-member-error"), []byte(err.Error()+"\n"))
			}
		}()
		err = checkSMBStartIntent(root)
		if err != nil {
			return err
		}
		err = checkSMBCTDBHealth(requestCtx, root, false)
		if err != nil {
			return err
		}
		switch op {
		case 'P':
			return nil
		case 'J':
			if attempted || child != nil {
				return fmt.Errorf("AD join already attempted or member running")
			}
			attempted = true
			return runSMBADJoin(requestCtx, root)
		case 'S':
			return start(requestCtx)
		case 'R':
			if child == nil {
				return fmt.Errorf("Winbind is not started")
			}
			err = checkSMBADReady(requestCtx, root)
			if err != nil {
				return err
			}
			err = os.Remove(filepath.Join(root, "ad-initializing"))
			if os.IsNotExist(err) {
				return nil
			}
			return err
		default:
			return fmt.Errorf("unsupported AD member operation")
		}
	})
}

type smbADOutput struct{ bytes.Buffer }

func (b *smbADOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 1024*1024 - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

// Bound the whole child process group, including upstream Joiner's net child.
// Neither argv nor public errors contain credentials/output. Failed output is
// bounded and retained only under the private cluster diagnostics directory.
func runSMBADProcess(ctx context.Context, root string, args []string, input io.Reader) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing AD command")
	}
	err := ctx.Err()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out smbADOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Stdin = input
	err = cmd.Run()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = writeSMBNativeFile(filepath.Join(root, "diagnostics/ad-process-error"), out.Bytes())
		return nil, fmt.Errorf("AD operation failed or timed out; inspect private diagnostics; external outcome may be uncertain")
	}
	return out.Bytes(), nil
}

func smbADCTDB(ctx context.Context, root string, args ...string) ([]byte, error) {
	// The pinned CLI has no socket option. The Winbind namespace bind-mounts
	// this cluster's private run directory at the default /run/ctdb path.
	argv := []string{filepath.Join(os.Getenv("SNAP"), "bin/ctdb"), "-t", "5"}
	return runSMBADProcess(ctx, root, append(argv, args...), nil)
}

func smbADRecord(ctx context.Context, root, db, key string) error {
	if db == "autorid.tdb" {
		// Samba terminates this key with NUL; CTDB decodes only 0x-prefixed
		// hex, not backslash escapes. Never put a literal NUL in argv.
		key = "0x" + hex.EncodeToString([]byte(key+"\x00"))
	}
	raw, err := smbADCTDB(ctx, root, "pfetch", db, key)
	if err != nil {
		return fmt.Errorf("AD trust/idmap unavailable; retain state and use manual maintenance, never rejoin")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("AD trust/idmap state lost; manual maintenance required, refusing reconstruction")
	}
	return nil
}

func checkSMBADMember(ctx context.Context, root string, initial bool) error {
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD == nil || !launch.Clustered {
		return fmt.Errorf("AD member context required")
	}
	for _, key := range []string{"SECRETS/MACHINE_PASSWORD/", "SECRETS/SID/"} {
		err = smbADRecord(ctx, root, "secrets.tdb", key+launch.AD.Workgroup)
		if err != nil {
			return err
		}
	}
	if initial {
		raw, readErr := os.ReadFile(filepath.Join(root, "ad-initializing"))
		if readErr == nil {
			expected, _ := json.Marshal(launch.AD)
			if !bytes.Equal(raw, expected) {
				return fmt.Errorf("AD initialization identity mismatch")
			}
			return nil // First mapping initialization is allowed only before any serving.
		}
		if !os.IsNotExist(readErr) {
			return readErr
		}
	}
	return smbADRecord(ctx, root, "autorid.tdb", "CONFIG")
}

func runSMBADJoin(ctx context.Context, root string) (retErr error) {
	defer func() {
		if retErr == nil {
			return
		}
		directory := filepath.Join(root, "diagnostics")
		err := smbPrivateDir(directory)
		if err != nil {
			return
		}
		// Retain the first initial-attempt failure independently of health probes.
		file, err := os.OpenFile(filepath.Join(directory, "ad-initial-join-error"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return
		}
		defer file.Close()
		detail, _ := os.ReadFile(filepath.Join(directory, "ad-process-error"))
		_, _ = file.Write(append([]byte(retErr.Error()+"\n"), detail...))
	}()

	ctx, cancel := context.WithTimeout(ctx, smbADJoinTimeout)
	defer cancel()
	// Do not reinterpret unexpected preexisting identity as a new installation.
	// getdbmap is non-creating; pfetch/catdb cannot be used to infer safe absence.
	raw, err := smbADCTDB(ctx, root, "getdbmap")
	if err != nil {
		return err
	}
	for _, db := range []string{"secrets.tdb", "autorid.tdb", "winbindd_idmap.tdb"} {
		if strings.Contains(string(raw), "name:"+db+" ") {
			return fmt.Errorf("retained AD databases require manual maintenance; refusing initial join")
		}
	}
	raw, err = os.ReadFile(filepath.Join(root, "config/candidate.json"))
	if err != nil {
		return err
	}
	var candidate smbCandidate
	err = json.Unmarshal(raw, &candidate)
	if err != nil {
		return fmt.Errorf("invalid private AD candidate")
	}
	// Reuse the complete pinned-binding policy checks before the external effect.
	document, err := smbCombinedDocument(candidate)
	if err != nil {
		return err
	}
	ordered, err := smbOrderedBindingDocument(candidate.Config, document, nil)
	if err != nil {
		return err
	}
	var description smbBindingDescription
	err = smbPythonBinding(ctx, root)("inspect", smbBindingRequest{ClusterID: candidate.Spec.ClusterID, Config: ordered}, &description)
	if err != nil {
		return err
	}
	member, err := smbMemberContext(candidate, description)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("AD member context required")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(launch.AD)
	actual, _ := json.Marshal(member)
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("AD launch identity does not match candidate")
	}
	request := struct {
		ConfigPath string            `json:"config_path"`
		Joins      []json.RawMessage `json:"joins"`
	}{filepath.Join(root, "config/smb.conf"), candidate.Joins}
	raw, err = json.Marshal(request)
	if err != nil {
		return err
	}
	// Stdin goes to Python; pinned Joiner sends only the password to net stdin.
	_, err = runSMBADProcess(ctx, root, []string{filepath.Join(os.Getenv("SNAP"), "bin/python3"), "-B", filepath.Join(os.Getenv("SNAP"), "smb/native.py"), "join", "-"}, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	err = checkSMBADTrust(ctx, root)
	if err != nil {
		return err
	}
	return writeSMBNativeFile(filepath.Join(root, "ad-initializing"), actual)
}

func checkSMBADTrust(ctx context.Context, root string) error {
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD == nil {
		return fmt.Errorf("AD member context required")
	}
	for _, key := range []string{"SECRETS/MACHINE_PASSWORD/", "SECRETS/SID/"} {
		err = smbADRecord(ctx, root, "secrets.tdb", key+launch.AD.Workgroup)
		if err != nil {
			return err
		}
	}
	return nil
}

var smbADSIDPattern = regexp.MustCompile(`(?m)^SID\s*:\s*(S-1-5-21-[0-9]+-[0-9]+-[0-9]+)\s*$`)

func checkSMBADReady(ctx context.Context, root string) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD == nil {
		return fmt.Errorf("AD member context required")
	}
	// Check retained allocations before any lookup could create fresh ranges.
	// The initial actor's permission expires on its first successful readiness.
	err = checkSMBADMember(ctx, root, true)
	if err != nil {
		return err
	}
	// This is a live trust test, not proof of any SMB/Kerberos client protocol.
	wbinfo := func(args ...string) ([]byte, error) {
		return runSMBADProcess(ctx, root, append([]string{filepath.Join(os.Getenv("SNAP"), "bin/wbinfo")}, args...), nil)
	}
	_, err = wbinfo("--ping")
	if err != nil {
		return err
	}
	_, err = wbinfo("--check-secret")
	if err != nil {
		return err
	}
	raw, err := wbinfo("--domain-info=" + launch.AD.Workgroup)
	if err != nil {
		return err
	}
	sid := smbADSIDPattern.FindSubmatch(raw)
	if len(sid) != 2 {
		return fmt.Errorf("Winbind did not resolve the member domain SID")
	}
	// RID 513 is the domain's primary users group, independent of its local name.
	raw, err = wbinfo("--sid-to-gid=" + string(sid[1]) + "-513")
	if err != nil {
		return err
	}
	gid := strings.TrimSpace(string(raw))
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(gid) {
		return fmt.Errorf("invalid Winbind group mapping")
	}
	raw, err = runSMBADProcess(ctx, root, []string{filepath.Join(os.Getenv("SNAP"), "bin/getent"), "group", gid}, nil)
	if err != nil {
		return err
	}
	fields := strings.Split(strings.TrimSpace(string(raw)), ":")
	if len(fields) != 4 || fields[2] != gid {
		return fmt.Errorf("app-private NSS group lookup is not ready")
	}
	return checkSMBADMember(ctx, root, false)
}
