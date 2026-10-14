package ceph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var errSMBSourceMissing = errors.New("SMB source does not exist")

type smbSpec struct {
	ClusterID       string            `json:"cluster_id"`
	Features        []string          `json:"features"`
	ClusterMetaURI  string            `json:"cluster_meta_uri"`
	ClusterLockURI  string            `json:"cluster_lock_uri"`
	ConfigURI       string            `json:"config_uri"`
	UserSources     []string          `json:"user_sources"`
	JoinSources     []string          `json:"join_sources"`
	CustomDNS       []string          `json:"custom_dns"`
	PublicAddresses []json.RawMessage `json:"cluster_public_addrs"`
	CustomPorts     map[string]int    `json:"custom_ports"`
	BindAddresses   []json.RawMessage `json:"bind_addrs"`
	TLSCert         string            `json:"remote_control_ssl_cert"`
	TLSKey          string            `json:"remote_control_ssl_key"`
	TLSCA           string            `json:"remote_control_ca_cert"`
}

type smbCandidate struct {
	CTDB       *database.SMBCTDBView `json:"ctdb,omitempty"`
	Logical    json.RawMessage       `json:"logical"`
	CephConfig string                `json:"ceph_config"`
	Spec       smbSpec               `json:"spec"`
	SpecBytes  json.RawMessage       `json:"spec_document"`
	Config     json.RawMessage       `json:"config"`
	Users      []json.RawMessage     `json:"users"`
	Joins      []json.RawMessage     `json:"joins"`
	TLS        map[string]string     `json:"tls"`
}

func (c smbCandidate) configurationFingerprint() string {
	c.CTDB = nil
	// Python publication sorts JSON keys. Compare semantic source values rather
	// than RawMessage whitespace/order introduced at the compiler boundary.
	raw, _ := json.Marshal(c)
	var value any
	_ = json.Unmarshal(raw, &value)
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (c smbCandidate) fingerprint() string {
	// Raw secret bytes are included privately, never returned in status/inventory.
	data, _ := json.Marshal(c)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s smbSpec) hasFeature(feature string) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

func readSMBCandidate(clusterID string, read func(string) ([]byte, error)) (smbCandidate, error) {
	c := smbCandidate{TLS: map[string]string{}}
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return c, err
	}
	prefix := "rados://.smb/" + clusterID + "/"
	data, err := read(prefix + "spec.smb")
	if err != nil {
		return c, err
	}
	var transport struct {
		ServiceType string  `json:"service_type"`
		ServiceID   string  `json:"service_id"`
		Spec        smbSpec `json:"spec"`
	}
	err = json.Unmarshal(data, &transport)
	if err != nil {
		return c, fmt.Errorf("invalid SMB spec document")
	}
	c.Spec = transport.Spec
	c.SpecBytes = data
	if transport.ServiceType != "smb" || transport.ServiceID != clusterID || c.Spec.ClusterID != clusterID {
		return c, fmt.Errorf("SMB spec cluster identity mismatch")
	}
	if len(c.Spec.PublicAddresses) > 0 {
		return c, fmt.Errorf("public addresses require a separate HA/LB implementation")
	}
	uris := []string{c.Spec.ConfigURI}
	uris = append(uris, c.Spec.UserSources...)
	uris = append(uris, c.Spec.JoinSources...)
	for _, uri := range []string{c.Spec.TLSCert, c.Spec.TLSKey, c.Spec.TLSCA} {
		if uri != "" {
			uris = append(uris, uri)
		}
	}
	for _, uri := range uris {
		err = validateSMBSourceURI(clusterID, uri)
		if err != nil {
			return c, err
		}
	}
	c.Config, err = read(c.Spec.ConfigURI)
	if errors.Is(err, errSMBSourceMissing) && c.Spec.ConfigURI == prefix+"config.smb.override" {
		c.Config, err = read(prefix + "config.smb")
	}
	if err != nil {
		return c, err
	}
	// Observe override creation/removal even when spec.smb has not been regenerated.
	override, err := read(prefix + "config.smb.override")
	if err == nil {
		c.Config = override
	} else if !errors.Is(err, errSMBSourceMissing) {
		return c, err
	}
	if !json.Valid(c.Config) {
		return c, fmt.Errorf("invalid SMB configuration document")
	}
	for _, uri := range c.Spec.UserSources {
		data, err = read(uri)
		if err != nil {
			return c, err
		}
		if !json.Valid(data) {
			return c, fmt.Errorf("invalid SMB identity source")
		}
		c.Users = append(c.Users, json.RawMessage(data))
	}
	for _, uri := range c.Spec.JoinSources {
		data, err = read(uri)
		if err != nil {
			return c, err
		}
		if !json.Valid(data) {
			return c, fmt.Errorf("invalid SMB join source")
		}
		c.Joins = append(c.Joins, json.RawMessage(data))
	}
	for role, uri := range map[string]string{"cert": c.Spec.TLSCert, "key": c.Spec.TLSKey, "ca": c.Spec.TLSCA} {
		if uri == "" {
			continue
		}
		data, err = read(uri)
		if err != nil {
			return c, err
		}
		c.TLS[role] = string(data)
	}
	return c, nil
}

func smbLogicalPresent(clusterID string, data []byte) (bool, error) {
	var doc struct {
		ResourceType    string             `json:"resource_type"`
		PublicAddresses []json.RawMessage  `json:"public_addrs"`
		ClusterID       string             `json:"cluster_id"`
		Resources       *[]json.RawMessage `json:"resources"`
	}
	err := json.Unmarshal(data, &doc)
	if err != nil {
		return false, fmt.Errorf("invalid logical SMB response")
	}
	if len(doc.PublicAddresses) > 0 {
		return false, fmt.Errorf("public addresses require a separate HA/LB implementation")
	}
	if doc.Resources != nil && len(*doc.Resources) == 0 {
		return false, nil
	}
	if doc.ResourceType == "ceph.smb.cluster" && doc.ClusterID == clusterID {
		return true, nil
	}
	return false, fmt.Errorf("logical SMB cluster response does not match requested identity")
}

// readSMBPreparedCandidate shares the read-only source and mode validation used
// by placement and refresh. It never creates identities or reserves ownership.
func readSMBPreparedCandidate(ctx context.Context, s interfaces.StateInterface, clusterID, root string, logical []byte, withView bool) (smbCandidate, error) {
	read := func(uri string) ([]byte, error) { return smbReadSource(ctx, clusterID, root, uri) }
	candidate, err := readSMBCandidate(clusterID, read)
	if err != nil {
		return candidate, err
	}
	cephConfig, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "ceph.conf"))
	if err != nil {
		return candidate, err
	}
	candidate.CephConfig = string(cephConfig)
	candidate.Logical = json.RawMessage(logical)
	_, err = smbCoreRoles(candidate.Spec, false)
	if err != nil {
		return candidate, err
	}
	if candidate.Spec.hasFeature("clustered") {
		for _, uri := range []string{candidate.Spec.ClusterMetaURI, candidate.Spec.ClusterLockURI} {
			err = validateSMBSourceURI(clusterID, uri)
			if err != nil || !strings.HasPrefix(uri, "rados://.smb/"+clusterID+"/") {
				return candidate, fmt.Errorf("native CTDB requires selected cluster RADOS metadata/lock sources")
			}
		}
		if candidate.Spec.ClusterMetaURI == candidate.Spec.ClusterLockURI {
			return candidate, fmt.Errorf("native CTDB metadata/lock objects must be distinct")
		}
	}
	if len(candidate.Spec.BindAddresses) > 0 {
		return candidate, fmt.Errorf("native bind-address planning requires the network feature stage")
	}
	if withView && candidate.Spec.hasFeature("clustered") {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			candidate.CTDB, err = database.GetSMBCTDBView(ctx, tx, clusterID, s.ClusterState().Name())
			return err
		})
	}
	return candidate, err
}

type smbLaunch struct {
	Proxy     bool `json:"proxy"`
	Clustered bool `json:"clustered"`
	SMBPort   int  `json:"smb_port"`
}

// prepareSMBProjection uses the same real compiler, source recheck and nested
// context consumption for explicit startup and configuration-only refresh.
func prepareSMBProjection(ctx context.Context, s interfaces.StateInterface, candidate smbCandidate, root string) (smbLaunch, []string, error) {
	launch := smbLaunch{}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return launch, nil, err
	}
	err = smbAtomicWrite(filepath.Join(root, "config", "candidate.next.json"), raw)
	if err != nil {
		return launch, nil, err
	}
	err = smbNativeCommand(ctx, "compile", root)
	if err != nil {
		return launch, nil, err
	}
	recheck, err := readSMBPreparedCandidate(ctx, s, candidate.Spec.ClusterID, root, candidate.Logical, candidate.CTDB != nil)
	if err != nil {
		return launch, nil, err
	}
	if recheck.fingerprint() != candidate.fingerprint() {
		return launch, nil, fmt.Errorf("SMB sources changed during preparation; retry")
	}
	contextBytes, err := os.ReadFile(filepath.Join(root, "config", "next", "config", "context.json"))
	if err != nil {
		return launch, nil, err
	}
	err = json.Unmarshal(contextBytes, &launch)
	if err != nil {
		return launch, nil, err
	}
	if launch.Clustered != candidate.Spec.hasFeature("clustered") {
		return launch, nil, fmt.Errorf("native SMB launch mode does not match reservation")
	}
	roles, err := smbCoreRoles(candidate.Spec, launch.Proxy)
	return launch, roles, err
}
