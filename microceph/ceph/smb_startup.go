package ceph

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

// Startup requirements are part of the existing Go-owned context projection,
// not a second manifest or an authorization source independent of SQL.
type smbRequiredRecord struct {
	// Database names an existing native CTDB database.
	Database string `json:"database"`
	// Key is the native CLI representation, including NUL-terminated hex keys.
	Key string `json:"key"`
}

type smbStartupChecks struct {
	// Prepared permits this Go-prepared projection to start independently.
	Prepared bool `json:"prepared"`
	// Initialized fences mapping creation attempts, not confirmed SQL completion.
	Initialized bool `json:"initialized,omitempty"`
	// Records are asserted again on every independent native service start.
	Records []smbRequiredRecord `json:"records,omitempty"`
}

var smbContextMu sync.Mutex

func smbADRequiredRecords(member *smbADMember, strict bool) []smbRequiredRecord {
	records := []smbRequiredRecord{
		{Database: "secrets.tdb", Key: "SECRETS/MACHINE_PASSWORD/" + member.Workgroup},
		{Database: "secrets.tdb", Key: "SECRETS/SID/" + member.Workgroup},
	}
	if strict {
		records = append(records, smbRequiredRecord{Database: "autorid.tdb", Key: "0x" + hex.EncodeToString([]byte("CONFIG\x00"))})
	}
	return records
}

func smbDefaultStartup(launch smbLaunch) map[string]smbStartupChecks {
	startup := map[string]smbStartupChecks{"smbd": {}}
	if launch.AD != nil {
		startup["smbd"] = smbStartupChecks{Records: smbADRequiredRecords(launch.AD, true)}
		startup["winbindd"] = smbStartupChecks{Records: smbADRequiredRecords(launch.AD, true)}
	}
	return startup
}

// publishSMBContext serializes context publication with first-readiness
// tightening. Completion already persisted locally can never be downgraded by
// a stale SQL initialization result or a new config projection.
func publishSMBContext(root string, raw []byte, sameConfiguration bool) error {
	smbContextMu.Lock()
	defer smbContextMu.Unlock()
	var next smbLaunch
	err := json.Unmarshal(raw, &next)
	if err != nil {
		return err
	}
	if next.Startup == nil {
		next.Startup = smbDefaultStartup(next)
	}
	current, err := loadSMBLaunchForPublication(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && reflect.DeepEqual(current.AD, next.AD) {
		if sameConfiguration && current.Startup != nil {
			next.Startup = current.Startup
		} else {
			for role, checks := range current.Startup {
				if checks.Initialized {
					updated := next.Startup[role]
					updated.Initialized = true
					next.Startup[role] = updated
				}
			}
		}
	}
	raw, err = json.Marshal(next)
	if err != nil {
		return err
	}
	return writeSMBNativeFile(filepath.Join(root, "config/context.json"), raw)
}

func setSMBPrepared(root, role string, initial bool) error {
	if role != "smbd" && role != "winbindd" {
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
	if launch.Startup == nil {
		launch.Startup = smbDefaultStartup(launch)
	}
	checks := launch.Startup[role]
	checks.Prepared = true
	if launch.AD != nil {
		if role == "smbd" || !initial || checks.Initialized {
			checks.Initialized = true
			checks.Records = smbADRequiredRecords(launch.AD, true)
		} else {
			checks.Records = smbADRequiredRecords(launch.AD, false)
		}
	} else if role == "winbindd" {
		return fmt.Errorf("Winbind requires AD context")
	}
	launch.Startup[role] = checks
	return writeSMBLaunchState(root, launch)
}

// Fence before an allocating request, not after a potentially lost reply. A
// status request cannot turn an incomplete publication into prepared state.
func advanceSMBADStartup(root string, serving bool) error {
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
	checks := launch.Startup["winbindd"]
	if launch.AD == nil || !checks.Prepared {
		return fmt.Errorf("AD runtime is not prepared")
	}
	if serving && !checks.Initialized {
		return fmt.Errorf("AD initialization was not fenced")
	}
	checks.Initialized = true
	checks.Records = smbADRequiredRecords(launch.AD, true)
	launch.Startup["winbindd"] = checks
	if serving {
		launch.Startup["smbd"] = smbStartupChecks{Prepared: true, Initialized: true, Records: smbADRequiredRecords(launch.AD, true)}
	}
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
