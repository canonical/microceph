package ceph

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	smbADJoinTimeout      = time.Minute
	smbADReadinessTimeout = 30 * time.Second
)

var smbADProbeFunc = probeSMBADRuntime
var smbADJoinFunc = runSMBADJoin
var smbADPrepareFunc = prepareSMBADStart
var smbADReadyFunc = readySMBADRuntime

func probeSMBADRuntime(ctx context.Context, root string) error {
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	// The independently snap-started wrapper creates only safe empty stubs.
	// Observe them before consuming SQL authorization; do not create them from
	// the daemon or mistake host Winbind for the owner-local service.
	for _, path := range []string{"/run/samba", "/run/samba/winbindd"} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe Winbind runtime mountpoint")
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if path != "/run/samba" || entry.Name() != "winbindd" {
				return fmt.Errorf("Winbind runtime mountpoint contains unrelated state")
			}
		}
	}
	return checkSMBCTDBHealth(ctx, root, false)
}

func prepareSMBADStart(ctx context.Context, root string, initial bool) error {
	err := checkSMBADMember(ctx, root, initial)
	if err != nil {
		return err
	}
	_, err = os.Stat(filepath.Join(root, "ad-initializing"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// This publishes prepared data, not a request to a helper supervisor.
	// The snap-started wrapper can now assert it and exec native Winbind.
	return setSMBPrepared(root, "winbindd", initial && err == nil)
}

func readySMBADRuntime(ctx context.Context, root string) error {
	ctx, cancel := context.WithTimeout(ctx, smbADReadinessTimeout)
	defer cancel()
	err := checkSMBCTDBHealth(ctx, root, false)
	if err != nil {
		return err
	}
	err = checkSMBADReady(ctx, root)
	if err != nil {
		return err
	}
	// Tighten durable independent-startup requirements before acknowledging
	// readiness or removing the one-time initialization permission.
	err = advanceSMBADStartup(root, true)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(root, "ad-initializing"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
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

// Bound the whole transient group, including Joiner's net child, without
// binding the lifetime of native Winbind to this daemon or request.
func runSMBADProcess(ctx context.Context, root string, args []string, input io.Reader) ([]byte, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}
	cmd, err := newSMBPrivateCommandFunc(ctx, root, args)
	if err != nil {
		return nil, err
	}
	var out smbADOutput
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &out, input
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
	argv := []string{filepath.Join(os.Getenv("SNAP"), "bin/ctdb"), "-t", "5"}
	return runSMBADProcess(ctx, root, append(argv, args...), nil)
}

func smbADRecord(ctx context.Context, root, db, key string) error {
	inventory, err := smbADCTDB(ctx, root, "getdbmap")
	if err != nil {
		return fmt.Errorf("AD trust/idmap database unavailable; refusing attachment or reconstruction")
	}
	return smbADRecordFromInventory(ctx, root, inventory, db, key)
}

func smbADRecordFromInventory(ctx context.Context, root string, inventory []byte, db, key string) error {
	if !strings.Contains(string(inventory), "name:"+db+" ") {
		return fmt.Errorf("AD trust/idmap database unavailable; refusing attachment or reconstruction")
	}
	if db == "autorid.tdb" {
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
	// Inventory is non-creating. Reuse it only within this observation rather
	// than starting a fresh private namespace for every required record.
	// Readiness still takes a fresh observation after the allocating SID lookup.
	inventory, err := smbADCTDB(ctx, root, "getdbmap")
	if err != nil {
		return fmt.Errorf("AD trust/idmap database unavailable; refusing attachment or reconstruction")
	}
	for _, key := range []string{"SECRETS/MACHINE_PASSWORD/", "SECRETS/SID/"} {
		err = smbADRecordFromInventory(ctx, root, inventory, "secrets.tdb", key+launch.AD.Workgroup)
		if err != nil {
			return err
		}
	}
	if launch.Startup["winbindd"].Initialized {
		initial = false
	}
	if initial {
		raw, readErr := os.ReadFile(filepath.Join(root, "ad-initializing"))
		if readErr == nil {
			expected, _ := json.Marshal(launch.AD)
			if !bytes.Equal(raw, expected) {
				return fmt.Errorf("AD initialization identity mismatch")
			}
			return nil
		}
		if !os.IsNotExist(readErr) {
			return readErr
		}
	}
	return smbADRecordFromInventory(ctx, root, inventory, "autorid.tdb", "CONFIG")
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
	// getdbmap is non-creating. Never use pfetch to infer safe absence.
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
	ctx, cancel := context.WithTimeout(ctx, smbADReadinessTimeout)
	defer cancel()
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.AD == nil {
		return fmt.Errorf("AD member context required")
	}
	if !launch.Startup["winbindd"].Prepared {
		return fmt.Errorf("AD runtime is not prepared")
	}
	err = checkSMBADMember(ctx, root, true)
	if err != nil {
		return err
	}
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
	err = advanceSMBADStartup(root, false)
	if err != nil {
		return err
	}
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
