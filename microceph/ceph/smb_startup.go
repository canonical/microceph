package ceph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Preparation is Go-owned; independent starts may only consume it.
type smbStartupChecks struct {
	Prepared bool `json:"prepared"`
}

var smbContextMu sync.Mutex

func smbDefaultStartup() map[string]smbStartupChecks {
	return map[string]smbStartupChecks{"smbd": {}}
}

// publishSMBContext invalidates preparation before any live input can advance.
// Even re-enable with identical inputs imports SAM with smbd confirmed stopped.
func publishSMBContext(root string, raw []byte) error {
	smbContextMu.Lock()
	defer smbContextMu.Unlock()
	next, err := decodeSMBLaunch(raw)
	if err != nil {
		return err
	}
	next.Startup = smbDefaultStartup()
	raw, err = json.Marshal(next)
	if err != nil {
		return err
	}
	return writeSMBNativeFile(filepath.Join(root, "config/context.json"), raw)
}

func setSMBPrepared(root, role string) error {
	if role != "smbd" {
		return fmt.Errorf("unsupported SMB startup role")
	}
	smbContextMu.Lock()
	defer smbContextMu.Unlock()
	active, err := activeSMBRoot(filepath.Dir(root))
	if err != nil || active != root {
		return fmt.Errorf("SMB preparation owner changed")
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	launch.Startup = map[string]smbStartupChecks{"smbd": {Prepared: true}}
	return writeSMBLaunchState(root, launch)
}

func writeSMBLaunchState(root string, launch smbLaunch) error {
	raw, err := json.Marshal(launch)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "config/context.json")
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Equal(current, raw) {
		return nil
	}
	return writeSMBNativeFile(path, raw)
}
