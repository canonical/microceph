package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ReserveSMBADOwner reserves an exact clustered owner with immutable AD identity.
func ReserveSMBADOwner(ctx context.Context, tx *sql.Tx, clusterID, member, identity string) (uint64, error) {
	if !validSMBADIdentity(identity) {
		return 0, fmt.Errorf("invalid AD identity")
	}
	return reserveSMBOwner(ctx, tx, clusterID, member, true, identity)
}

func validSMBADIdentity(identity string) bool {
	var tuple struct {
		Realm       string `json:"realm"`
		Workgroup   string `json:"workgroup"`
		NetBIOSName string `json:"netbios_name"`
		FQDN        string `json:"fqdn"`
	}
	decoder := json.NewDecoder(strings.NewReader(identity))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&tuple)
	if err != nil || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	short := regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,14}$`)
	if !short.MatchString(tuple.Workgroup) || !short.MatchString(tuple.NetBIOSName) || len(tuple.Realm) > 253 || len(tuple.FQDN) > 253 || len(strings.Split(tuple.Realm, ".")) < 2 {
		return false
	}
	label := regexp.MustCompile(`^[A-Z0-9]([A-Z0-9-]{0,61}[A-Z0-9])?$`)
	for _, part := range strings.Split(tuple.Realm, ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	if tuple.Realm == "" || tuple.Workgroup == "" || tuple.NetBIOSName == "" {
		return false
	}
	if tuple.Realm != strings.ToUpper(tuple.Realm) || tuple.Workgroup != strings.ToUpper(tuple.Workgroup) || tuple.NetBIOSName != strings.ToUpper(tuple.NetBIOSName) {
		return false
	}
	if tuple.FQDN != strings.ToLower(tuple.NetBIOSName+"."+tuple.Realm) {
		return false
	}
	canonical, err := json.Marshal(tuple)
	return err == nil && string(canonical) == identity
}

func validateSMBADHistory(config SMBServiceGroupConfig) error {
	if config.AuthIdentity == "" && config.AD == nil {
		return nil
	}
	if !config.Clustered || !validSMBADIdentity(config.AuthIdentity) || config.AD == nil {
		return fmt.Errorf("missing or conflicting retained AD identity/history; manual maintenance required")
	}
	state := config.AD
	if state.Initialized && !state.Joined || state.Joined && state.JoinOwner == "" {
		return fmt.Errorf("invalid retained AD initialization history")
	}
	if state.JoinOwner != "" {
		actor, err := strconv.ParseInt(state.JoinOwner, 10, 64)
		_, ranked := config.Ranks[state.JoinOwner]
		if err != nil || actor <= 0 || strconv.FormatInt(actor, 10) != state.JoinOwner || !ranked {
			return fmt.Errorf("AD join actor has lost retained rank identity")
		}
	}
	return nil
}

// UnmarshalJSON refuses partial or unknown AD history instead of inferring fresh state.
func (s *SMBADState) UnmarshalJSON(raw []byte) error {
	var fields struct {
		JoinOwner   *string `json:"join_owner"`
		Joined      *bool   `json:"joined"`
		Initialized *bool   `json:"initialized"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&fields)
	if err != nil || fields.JoinOwner == nil || fields.Joined == nil || fields.Initialized == nil {
		return fmt.Errorf("incomplete retained AD history; manual maintenance required")
	}
	*s = SMBADState{JoinOwner: *fields.JoinOwner, Joined: *fields.Joined, Initialized: *fields.Initialized}
	return nil
}

// SMBADState is the narrow initial-join record, never an expiring placement lease.
// A nonempty JoinOwner without Joined is an uncertain external effect. It is
// deliberately not cleared by stop, retirement, time, credential refresh or retry.
// Samba owns all passwords and mappings; no secrets are kept in this record.
type SMBADState struct {
	JoinOwner   string `json:"join_owner"`
	Joined      bool   `json:"joined"`
	Initialized bool   `json:"initialized"`
}

func smbADOwner(ctx context.Context, tx *sql.Tx, clusterID, member string) (int64, SMBServiceGroupConfig, string, error) {
	identity, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return 0, SMBServiceGroupConfig{}, "", err
	}
	id, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return 0, config, "", err
	}
	if !config.Clustered || config.AD == nil || config.Owners[identity] != member {
		return 0, config, "", fmt.Errorf("AD requires an exact clustered owner")
	}
	return id, config, identity, nil
}

// GetSMBADState observes join history for an exact retained owner.
func GetSMBADState(ctx context.Context, tx *sql.Tx, clusterID, member string) (SMBADState, error) {
	_, config, _, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return SMBADState{}, err
	}
	return *config.AD, nil
}

// BeginSMBADJoin consumes the sole initial attempt in a short write transaction.
// Commit before execution. Even the same actor cannot automatically retry.
func BeginSMBADJoin(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if config.AD.JoinOwner != "" || config.AD.Joined {
		return fmt.Errorf("AD join already attempted or uncertain; manual maintenance required, never retry on timeout")
	}
	config.AD.JoinOwner = identity
	return saveSMBGroup(ctx, tx, id, config)
}

// CompleteSMBADJoin records only the initiating actor's confirmed success.
func CompleteSMBADJoin(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if config.AD.JoinOwner != identity || config.AD.Joined {
		return fmt.Errorf("AD join actor does not match pending attempt")
	}
	config.AD.Joined = true
	return saveSMBGroup(ctx, tx, id, config)
}

// InitializeSMBAD records that trust, autorid and Winbind/NSS were checked before serving.
func InitializeSMBAD(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	id, config, identity, err := smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if !config.AD.Joined || (!config.AD.Initialized && config.AD.JoinOwner != identity) {
		return fmt.Errorf("AD initial state is not confirmed")
	}
	config.AD.Initialized = true
	return saveSMBGroup(ctx, tx, id, config)
}

// CheckSMBADPurge refuses deletion of the final retained AD identity replica.
// Caller must check in the same transaction as retirement. Local AD persistent
// databases are retained even on peer purge; this is not a backup guarantee.
func CheckSMBADPurge(ctx context.Context, tx *sql.Tx, clusterID, member string) error {
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	if config.AD == nil {
		return nil
	}
	_, config, _, err = smbADOwner(ctx, tx, clusterID, member)
	if err != nil {
		return err
	}
	if len(config.Owners) <= 1 || !config.AD.Initialized {
		return fmt.Errorf("refusing purge of final or uncertain AD identity state; manual maintenance required")
	}
	return nil
}

// HasSMBADIdentity identifies retained AD state, including after owner retirement.
func HasSMBADIdentity(ctx context.Context, tx *sql.Tx, clusterID string) (bool, error) {
	_, config, err := smbGroup(ctx, tx, clusterID)
	return config.AD != nil || config.AuthIdentity != "", err
}

// VerifySMBAuthIdentity checks the retained exact owner's immutable auth tuple.
// Empty identity means local-user mode, not permission to convert an AD group.
func VerifySMBAuthIdentity(ctx context.Context, tx *sql.Tx, clusterID, member, identity string) error {
	owner, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	if config.Owners[owner] != member || config.AuthIdentity != identity {
		return fmt.Errorf("SMB authentication identity changed or owner is uncertain")
	}
	return nil
}

// VerifySMBADRetainedIdentity requires preexisting AD history and the local
// member's retained rank before reservation. Retirement may release ownership,
// but local evidence never authorizes recreating lost SQL identity/history.
func VerifySMBADRetainedIdentity(ctx context.Context, tx *sql.Tx, clusterID, member, identity string) error {
	owner, err := smbMemberIdentity(ctx, tx, member)
	if err != nil {
		return err
	}
	_, config, err := smbGroup(ctx, tx, clusterID)
	if err != nil {
		return err
	}
	_, ranked := config.Ranks[owner]
	if config.AD == nil || config.AuthIdentity != identity || !ranked {
		return fmt.Errorf("retained AD identity/history or member rank is missing or changed")
	}
	return nil
}
