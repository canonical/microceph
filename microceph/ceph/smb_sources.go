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
	"strings"

	"github.com/canonical/microceph/microceph/common"
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

// smbEndpoint is resolved from upstream settings and local network inventory,
// not a member-local desired configuration. Omitted only on retained candidates.
type smbEndpoint struct {
	FrontendAddress string `json:"frontend_address"`
	SMBPort         int    `json:"smb_port"`
	MetricsPort     int    `json:"metrics_port"`
}

type smbCandidate struct {
	Endpoint   *smbEndpoint          `json:"endpoint,omitempty"`
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
	// Retained Python and current Go publications can differ in JSON encoding.
	// Compare semantic source values, not RawMessage whitespace/key ordering.
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
	_, err = c.Spec.endpointPorts()
	if err != nil {
		return c, err
	}
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
	endpoint, err := candidate.Spec.resolveEndpoint(ctx, s)
	if err != nil {
		return candidate, err
	}
	candidate.Endpoint = &endpoint
	if withView && candidate.Spec.hasFeature("clustered") {
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			candidate.CTDB, err = database.GetSMBCTDBView(ctx, tx, clusterID, s.ClusterState().Name())
			return err
		})
	}
	return candidate, err
}

// endpointPorts validates the complete map, including inactive services' defaults,
// just like the pinned resource validator. CTDB and remote-control are not tunable.
func (s smbSpec) endpointPorts() (smbEndpoint, error) {
	ports := map[string]int{"smb": 445, "smbmetrics": 9922, "ctdb": 4379, "remote-control": 54445}
	for name, port := range s.CustomPorts {
		defaultPort, known := ports[name]
		if !known || port < 1 || port > 65535 {
			return smbEndpoint{}, fmt.Errorf("invalid SMB custom port for service %q", name)
		}
		if (name == "ctdb" || name == "remote-control") && port != defaultPort {
			return smbEndpoint{}, fmt.Errorf("nondefault %s port is not supported", name)
		}
		ports[name] = port
	}
	seen := map[int]bool{}
	for _, port := range ports {
		if seen[port] {
			return smbEndpoint{}, fmt.Errorf("SMB service ports must be unique")
		}
		seen[port] = true
	}
	return smbEndpoint{SMBPort: ports["smb"], MetricsPort: ports["smbmetrics"]}, nil
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
			// ip_network in the upstream grammar also accepts a bare host literal.
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
		// The common helper validates the entire ordered Ceph subnet list.
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
	Proxy           bool           `json:"proxy"`
	Clustered       bool           `json:"clustered"`
	FrontendAddress string         `json:"frontend_address"`
	SMBPort         int            `json:"smb_port"`
	MetricsPort     int            `json:"metrics_port"`
	CustomDNS       bool           `json:"custom_dns"`
	CTDBPNN         *uint64        `json:"ctdb_pnn,omitempty"`
	DataChecks      []smbDataCheck `json:"data_checks"`
}

// serviceInfo exposes only successfully applied nonsecret facts. configURI is
// the validated source reference in the applied spec, not its contents or an
// assertion about override selection. Legacy endpoint omissions stay omitted.
func (l smbLaunch) serviceInfo(configURI string) string {
	info, _ := json.Marshal(struct {
		ConfigURI       string `json:"config_uri,omitempty"`
		Clustered       bool   `json:"clustered"`
		FrontendAddress string `json:"frontend_address,omitempty"`
		SMBPort         int    `json:"smb_port,omitempty"`
		MetricsAddress  string `json:"metrics_address,omitempty"`
		MetricsPort     int    `json:"metrics_port,omitempty"`
	}{configURI, l.Clustered, l.FrontendAddress, l.SMBPort, "0.0.0.0", l.MetricsPort})
	return string(info)
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
	err = compileSMBNativeFunc(ctx, root, candidate)
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
	if candidate.Endpoint == nil || launch.FrontendAddress != candidate.Endpoint.FrontendAddress || launch.SMBPort != candidate.Endpoint.SMBPort || launch.MetricsPort != candidate.Endpoint.MetricsPort {
		return launch, nil, fmt.Errorf("native SMB launch endpoint does not match preparation")
	}
	if launch.Clustered != candidate.Spec.hasFeature("clustered") {
		return launch, nil, fmt.Errorf("native SMB launch mode does not match reservation")
	}
	roles, err := smbCoreRoles(candidate.Spec, launch.Proxy)
	return launch, roles, err
}
