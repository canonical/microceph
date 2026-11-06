package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func decodeSMBSpec(clusterID string, data []byte) (smbSpec, error) {
	var transport struct {
		ServiceType string  `json:"service_type"`
		ServiceID   string  `json:"service_id"`
		Spec        smbSpec `json:"spec"`
	}
	err := json.Unmarshal(data, &transport)
	if err != nil {
		return smbSpec{}, fmt.Errorf("invalid SMB spec document")
	}
	spec := transport.Spec
	if transport.ServiceType != "smb" || transport.ServiceID != clusterID || spec.ClusterID != clusterID {
		return spec, fmt.Errorf("SMB spec cluster identity mismatch")
	}
	err = validateSMBLocalSpec(spec)
	return spec, err
}

func validateSMBLocalSpec(spec smbSpec) error {
	_, err := smbCoreRoles(spec)
	if err != nil {
		return err
	}
	_, err = spec.endpointPorts()
	if err != nil {
		return err
	}
	if len(spec.PublicAddresses) > 0 {
		return fmt.Errorf("public addresses require a separate HA/LB implementation")
	}
	if len(spec.JoinSources) > 0 || len(spec.CustomDNS) > 0 || spec.TLSCert != "" || spec.TLSKey != "" || spec.TLSCA != "" || spec.ClusterMetaURI != "" || spec.ClusterLockURI != "" {
		return fmt.Errorf("unsupported SMB domain, remote-control or clustered sources")
	}
	return nil
}

func validateSMBLogicalPolicy(logical []byte, spec smbSpec) error {
	err := validateSMBLocalSpec(spec)
	if err != nil {
		return err
	}
	var cluster struct {
		AuthMode   string `json:"auth_mode"`
		Clustering string `json:"clustering"`
	}
	err = json.Unmarshal(logical, &cluster)
	if err != nil {
		return fmt.Errorf("invalid logical SMB policy")
	}
	if cluster.AuthMode != "" && cluster.AuthMode != "user" {
		return fmt.Errorf("only local-user SMB authentication is supported")
	}
	if cluster.Clustering != "" && cluster.Clustering != "default" && cluster.Clustering != "never" {
		return fmt.Errorf("only nonclustered SMB is supported; use clustering: never")
	}
	return nil
}

// smbPolicyOptionName matches Samba's case-insensitive, ASCII-whitespace-ignoring
// parameter lookup (strwicmp), including spellings such as "serverrole".
func smbPolicyOptionName(key string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return -1
		}
		return r
	}, strings.ToLower(key))
}

func smbEffectiveGlobals(description smbBindingDescription) map[string]string {
	effective := make(map[string]string)
	for _, option := range description.Globals {
		effective[smbPolicyOptionName(option[0])] = strings.TrimSpace(option[1])
	}
	return effective
}

func validateSMBEffectivePolicy(spec smbSpec, description smbBindingDescription) error {
	err := validateSMBLocalSpec(spec)
	if err != nil {
		return err
	}
	if description.Clustered || description.DomainController {
		return fmt.Errorf("effective clustered or domain-controller mode is not supported")
	}
	// Sambacc preserves order; only the last Samba-equivalent option wins.
	effective := smbEffectiveGlobals(description)
	for _, key := range []string{"security", "realm", "serverrole", "clustering"} {
		value, present := effective[key]
		if !present {
			continue
		}
		switch key {
		case "security":
			security := smbPolicyOptionName(value)
			if security != "user" && security != "auto" {
				return fmt.Errorf("effective domain/security mode is not prepared")
			}
		case "realm":
			if value != "" {
				return fmt.Errorf("effective realm is not prepared")
			}
		case "serverrole":
			role := smbPolicyOptionName(value)
			if role != "auto" && role != "standalone" && role != "standaloneserver" {
				return fmt.Errorf("effective server role is not prepared")
			}
		case "clustering":
			switch smbPolicyOptionName(value) {
			case "no", "false", "off", "0":
			default:
				return fmt.Errorf("effective clustering override conflicts with generated spec")
			}
		}
	}
	role := smbPolicyOptionName(effective["serverrole"])
	if role == "" || role == "auto" {
		// Pinned Samba derives PDC/BDC from USER/AUTO plus domain logons.
		// Explicit standalone ignores these flags; the default is no logons.
		switch smbPolicyOptionName(effective["domainlogons"]) {
		case "yes", "true", "on", "1":
			return fmt.Errorf("effective automatic server role is not prepared; domain logons selects a controller")
		case "", "no", "false", "off", "0":
		default:
			return fmt.Errorf("effective domain logons value is invalid")
		}
	}
	return nil
}

// Sambacc exposes literal options, not Samba's expanded configuration. Never
// admit a second configuration source that can change policy after inspection.
func validateSMBDirectOptions(options [][2]string) error {
	for _, option := range options {
		key := smbPolicyOptionName(option[0])
		switch key {
		case "include", "copy", "configfile":
			return fmt.Errorf("unexpanded SMB configuration indirection is not supported: %s", key)
		case "configbackend":
			if smbPolicyOptionName(option[1]) != "file" {
				return fmt.Errorf("SMB configuration backend indirection is not supported")
			}
		case "registryshares":
			switch smbPolicyOptionName(option[1]) {
			case "no", "false", "off", "0":
			default:
				return fmt.Errorf("SMB registry share indirection is not supported")
			}
		}
	}
	return nil
}

func validateSMBPolicyDescription(candidate smbCandidate, description smbBindingDescription) error {
	err := validateSMBDirectOptions(description.Globals)
	if err != nil {
		return err
	}
	for _, share := range description.Shares {
		err = validateSMBDirectOptions(share.Options)
		if err != nil {
			return err
		}
		for _, option := range share.Options {
			key := smbPolicyOptionName(option[0])
			if key != "ceph_new:proxy" && key != "ceph:proxy" {
				continue
			}
			switch smbPolicyOptionName(option[1]) {
			case "yes", "true", "on", "1":
				return fmt.Errorf("CephFS proxy is not supported")
			}
		}
	}
	err = validateSMBEffectivePolicy(candidate.Spec, description)
	if err != nil {
		return err
	}
	if len(candidate.Logical) > 0 {
		err = validateSMBLogicalPolicy(candidate.Logical, candidate.Spec)
		if err != nil {
			return err
		}
	}
	return nil
}

var inspectSMBPolicyFunc = inspectSMBPolicy

func inspectSMBPolicy(ctx context.Context, root string, candidate smbCandidate) error {
	err := smbNativeRoot(root, candidate.Spec.ClusterID)
	if err != nil {
		return err
	}
	document, err := smbDecodeObject(candidate.Config)
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
		smbNativeFailure(root, &err)
		return err
	}
	return validateSMBPolicyDescription(candidate, description)
}

func writeSMBPolicyWarning(root, warning string) error {
	path := filepath.Join(root, "diagnostics", "policy-warning")
	if warning != "" {
		return smbAtomicWrite(path, []byte(warning))
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
