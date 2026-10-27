package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/common"
)

func prepareSMBAccounts(ctx context.Context, root string, initialize bool) error {
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
	err = validateSMBLocalSpec(candidate.Spec)
	if err != nil {
		return err
	}
	document, err := smbCombinedDocument(candidate)
	if err != nil {
		return err
	}
	if initialize {
		err = checkSMBFreshLocalSAM(root, false)
		if err == nil {
			err = smbAtomicWrite(filepath.Join(root, "identity/local-sam"), []byte(smbLocalSAMFence))
		}
	} else {
		err = checkSMBLocalSAM(root)
	}
	if err != nil {
		return err
	}
	err = ctx.Err()
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
	err = checkSMBLocalSAM(root)
	if err != nil {
		return err
	}
	return setSMBPrepared(root, "smbd")
}

func decodeSMBLaunch(data []byte) (smbLaunch, error) {
	var launch smbLaunch
	if !json.Valid(data) {
		return launch, fmt.Errorf("invalid SMB runtime context document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&launch)
	if err != nil {
		return launch, fmt.Errorf("invalid or unsupported SMB runtime context")
	}
	for role := range launch.Startup {
		if role != "smbd" {
			return launch, fmt.Errorf("unsupported SMB startup role")
		}
	}
	return launch, nil
}

func loadSMBLaunchForPublication(root string) (smbLaunch, error) {
	data, err := os.ReadFile(filepath.Join(root, "config/context.json"))
	if err != nil {
		return smbLaunch{}, err
	}
	return decodeSMBLaunch(data)
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

func smbReadyNative(ctx context.Context, root, role string) (retErr error) {
	if role != "smbd" {
		return fmt.Errorf("native readiness role is not prepared")
	}
	err := smbNativeRoot(root, filepath.Base(root))
	if err != nil {
		return err
	}
	defer smbNativeFailure(root, &retErr)
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil || active != root {
		return fmt.Errorf("SMB readiness owner mismatch")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if !launch.Startup[role].Prepared {
		return fmt.Errorf("SMB runtime is not prepared")
	}
	err = checkSMBLocalSAM(root)
	if err != nil {
		return err
	}
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
}
