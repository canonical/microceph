package ceph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var errSMBSourceMissing = errors.New("SMB source does not exist")

type smbSpec struct {
	ClusterID       string            `json:"cluster_id"`
	Features        []string          `json:"features"`
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
	Logical    json.RawMessage   `json:"logical"`
	CephConfig string            `json:"ceph_config"`
	Spec       smbSpec           `json:"spec"`
	SpecBytes  json.RawMessage   `json:"spec_document"`
	Config     json.RawMessage   `json:"config"`
	Users      []json.RawMessage `json:"users"`
	Joins      []json.RawMessage `json:"joins"`
	TLS        map[string]string `json:"tls"`
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
