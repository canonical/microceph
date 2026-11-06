package ceph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var errSMBSourceMissing = errors.New("SMB source does not exist")

type smbSpec struct {
	ClusterID     string            `json:"cluster_id"`
	Features      []string          `json:"features"`
	ConfigURI     string            `json:"config_uri"`
	UserSources   []string          `json:"user_sources"`
	CustomPorts   map[string]int    `json:"custom_ports"`
	BindAddresses []json.RawMessage `json:"bind_addrs"`
	// Unsupported-mode metadata is consumed only by fail-closed admission.
	ClusterMetaURI  string            `json:"cluster_meta_uri"`
	ClusterLockURI  string            `json:"cluster_lock_uri"`
	JoinSources     []string          `json:"join_sources"`
	CustomDNS       []string          `json:"custom_dns"`
	PublicAddresses []json.RawMessage `json:"cluster_public_addrs"`
	TLSCert         string            `json:"remote_control_ssl_cert"`
	TLSKey          string            `json:"remote_control_ssl_key"`
	TLSCA           string            `json:"remote_control_ca_cert"`
}

// smbEndpoint is resolved from upstream settings and local network inventory.
type smbEndpoint struct {
	FrontendAddress string `json:"frontend_address"`
	SMBPort         int    `json:"smb_port"`
}

func (s smbSpec) hasFeature(feature string) bool {
	for _, value := range s.Features {
		if value == feature {
			return true
		}
	}
	return false
}

type smbCandidate struct {
	CTDB       *database.SMBCTDBView `json:"ctdb,omitempty"`
	Endpoint   *smbEndpoint          `json:"endpoint,omitempty"`
	Logical    json.RawMessage       `json:"logical"`
	CephConfig string                `json:"ceph_config"`
	Spec       smbSpec               `json:"spec"`
	SpecBytes  json.RawMessage       `json:"spec_document"`
	Config     json.RawMessage       `json:"config"`
	Users      []json.RawMessage     `json:"users"`
}

// configurationFingerprint normalizes structural JSON ordering but preserves
// Samba option order: aliases may select different values when reordered.
// It is private: source documents contain credentials and never enter status.
func (c smbCandidate) configurationFingerprint() string {
	c.CTDB = nil
	raw, err := json.Marshal(c)
	if err != nil {
		return c.fingerprint()
	}
	var value map[string]any
	err = json.Unmarshal(raw, &value)
	if err != nil {
		return c.fingerprint()
	}
	document, err := smbDecodeObject(c.Config)
	if err != nil {
		return c.fingerprint()
	}
	ordered, err := smbOrderedBindingDocument(c.Config, document, nil)
	if err != nil {
		return c.fingerprint()
	}
	value["config"] = ordered
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (c smbCandidate) fingerprint() string {
	// Raw secret bytes are included privately, never in status/inventory.
	data, _ := json.Marshal(c)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func readSMBCandidate(clusterID string, read func(string) ([]byte, error)) (smbCandidate, error) {
	c := smbCandidate{}
	err := validateSMBClusterID(clusterID)
	if err != nil {
		return c, err
	}
	prefix := "rados://.smb/" + clusterID + "/"
	data, err := read(prefix + "spec.smb")
	if err != nil {
		return c, err
	}
	c.Spec, err = decodeSMBSpec(clusterID, data)
	if err != nil {
		return c, err
	}
	c.SpecBytes = data
	uris := append([]string{c.Spec.ConfigURI}, c.Spec.UserSources...)
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
	// Observe override creation/removal even before spec regeneration.
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

// readSMBPreparedCandidate never creates identities or reserves ownership.
func readSMBPreparedCandidateUnreserved(ctx context.Context, s interfaces.StateInterface, clusterID, root string, logical []byte) (smbCandidate, error) {
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
	err = validateSMBLogicalPolicy(logical, candidate.Spec)
	if err != nil {
		return candidate, err
	}
	endpoint, err := candidate.Spec.resolveEndpoint(ctx, s)
	if err != nil {
		return candidate, err
	}
	candidate.Endpoint = &endpoint
	return candidate, nil
}

func readSMBPreparedCandidate(ctx context.Context, s interfaces.StateInterface, clusterID, root string, logical []byte) (smbCandidate, error) {
	candidate, err := readSMBPreparedCandidateUnreserved(ctx, s, clusterID, root, logical)
	if err != nil {
		return candidate, err
	}
	if candidate.Spec.hasFeature("clustered") {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			candidate.CTDB, err = database.GetSMBCTDBView(ctx, tx, clusterID, s.ClusterState().Name())
			return err
		})
	}
	if err == nil && candidate.CTDB != nil && candidate.Endpoint.FrontendAddress == "" {
		candidate.Endpoint.FrontendAddress = candidate.CTDB.Member.Node
	}
	return candidate, err
}

func (s smbSpec) endpointPorts() (smbEndpoint, error) {
	port := 445
	for name, value := range s.CustomPorts {
		if name != "smb" || value < 1 || value > 65535 {
			return smbEndpoint{}, fmt.Errorf("invalid or unsupported SMB custom port for service %q", name)
		}
		port = value
	}
	return smbEndpoint{SMBPort: port}, nil
}

func (s smbSpec) resolveEndpoint(ctx context.Context, state interfaces.StateInterface) (smbEndpoint, error) {
	endpoint, err := s.endpointPorts()
	if err != nil {
		return endpoint, err
	}
	type selector struct {
		Address string `json:"address"`
		Network string `json:"network"`
	}
	selectors := []selector{}
	if s.BindAddresses != nil && len(s.BindAddresses) == 0 {
		return endpoint, fmt.Errorf("bind_addrs must not be empty")
	}
	// Validate every selector before choosing a locally usable alternative.
	for _, raw := range s.BindAddresses {
		var bind selector
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&bind)
		if err != nil || (bind.Address == "") == (bind.Network == "") {
			return endpoint, fmt.Errorf("SMB bind selector requires exactly one address or network")
		}
		if bind.Address != "" {
			ip := net.ParseIP(bind.Address)
			if ip == nil || !ip.IsGlobalUnicast() {
				return endpoint, fmt.Errorf("SMB bind address must be an unscoped unicast IP literal")
			}
			bind.Address = ip.String()
		} else {
			ip := net.ParseIP(bind.Network)
			if ip != nil {
				bits := 128
				if ip.To4() != nil {
					bits = 32
				}
				bind.Network = fmt.Sprintf("%s/%d", ip.String(), bits)
			}
			ip, network, parseErr := net.ParseCIDR(bind.Network)
			if parseErr != nil || !ip.Equal(network.IP) {
				return endpoint, fmt.Errorf("SMB bind network must be a canonical CIDR or IP literal")
			}
		}
		selectors = append(selectors, bind)
	}
	if len(selectors) == 0 {
		config, err := fetchConfigDb(ctx, state)
		if err != nil {
			return endpoint, fmt.Errorf("read SMB public_network: %w", err)
		}
		address, err := common.Network.FindIpOnSubnet(config["public_network"])
		if err != nil {
			return endpoint, fmt.Errorf("resolve SMB address on public_network: %w", err)
		}
		endpoint.FrontendAddress = address
		return endpoint, nil
	}
	for _, bind := range selectors {
		if bind.Address != "" {
			_, err = common.Network.FindNetworkAddress(bind.Address)
			if err == nil {
				endpoint.FrontendAddress = bind.Address
				return endpoint, nil
			}
		} else {
			address, err := common.Network.FindIpOnSubnet(bind.Network)
			if err == nil {
				endpoint.FrontendAddress = address
				return endpoint, nil
			}
		}
	}
	return endpoint, fmt.Errorf("no SMB bind selector matches a suitable local address")
}

type smbLaunch struct {
	Clustered       bool                        `json:"clustered,omitempty"`
	CTDBPNN         *uint64                     `json:"ctdb_pnn,omitempty"`
	Startup         map[string]smbStartupChecks `json:"startup,omitempty"`
	FrontendAddress string                      `json:"frontend_address"`
	SMBPort         int                         `json:"smb_port"`
	DataChecks      []smbDataCheck              `json:"data_checks"`
}

func (l smbLaunch) serviceInfo(configURI string) string {
	info, _ := json.Marshal(struct {
		Clustered       bool    `json:"clustered,omitempty"`
		CTDBPNN         *uint64 `json:"ctdb_pnn,omitempty"`
		ConfigURI       string  `json:"config_uri,omitempty"`
		FrontendAddress string  `json:"frontend_address,omitempty"`
		SMBPort         int     `json:"smb_port,omitempty"`
	}{l.Clustered, l.CTDBPNN, configURI, l.FrontendAddress, l.SMBPort})
	return string(info)
}

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
	err = compileSMBNativeFunc(ctx, root, candidate)
	if err != nil {
		return launch, nil, err
	}
	recheck, err := readSMBPreparedCandidate(ctx, s, candidate.Spec.ClusterID, root, candidate.Logical)
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
	launch, err = decodeSMBLaunch(contextBytes)
	if err != nil {
		return launch, nil, err
	}
	if candidate.Endpoint == nil || launch.FrontendAddress != candidate.Endpoint.FrontendAddress || launch.SMBPort != candidate.Endpoint.SMBPort {
		return launch, nil, fmt.Errorf("native SMB launch endpoint does not match preparation")
	}
	if launch.Clustered {
		previous, readErr := loadSMBLaunchForPublication(root)
		if readErr == nil && previous.Clustered {
			for _, name := range []string{"ctdb/ctdb.conf", "config/ctdb.json", "config/ctdb-rank", "config/ctdb-identity", "config/ctdb-address"} {
				before, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					return launch, nil, err
				}
				after, err := os.ReadFile(filepath.Join(root, "config/next", name))
				if err != nil {
					return launch, nil, err
				}
				if !bytes.Equal(before, after) {
					return launch, nil, fmt.Errorf("CTDB startup configuration changed; explicit maintenance required")
				}
			}
		} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return launch, nil, readErr
		}
	}
	roles, err := smbCoreRoles(candidate.Spec)
	return launch, roles, err
}
