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
	_, err = spec.endpointPorts()
	if err != nil {
		return spec, err
	}
	if len(spec.PublicAddresses) > 0 {
		return spec, fmt.Errorf("public addresses require a separate HA/LB implementation")
	}
	return spec, nil
}

func validateSMBLogicalPolicy(logical []byte, spec smbSpec) error {
	var cluster struct {
		AuthMode   string `json:"auth_mode"`
		Clustering string `json:"clustering"`
	}
	err := json.Unmarshal(logical, &cluster)
	if err != nil {
		return fmt.Errorf("invalid logical SMB policy")
	}
	clustered := spec.hasFeature("clustered")
	// Omitted/default mode is resolved by upstream placement, never by counting
	// native gateways. Only explicit modes can be compared directly here.
	if (cluster.Clustering == "always" && !clustered) || (cluster.Clustering == "never" && clustered) {
		return fmt.Errorf("logical SMB clustering does not match generated spec; wait for upstream configuration convergence")
	}
	if (cluster.AuthMode == "active-directory" || spec.hasFeature("domain")) && !clustered {
		return fmt.Errorf("Active Directory requires clustered SMB; use clustering: always")
	}
	if cluster.AuthMode == "active-directory" || spec.hasFeature("domain") {
		if cluster.AuthMode != "active-directory" || !spec.hasFeature("domain") {
			return fmt.Errorf("logical AD authentication does not match generated spec")
		}
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

func validateSMBEffectivePolicy(spec smbSpec, description smbBindingDescription) error {
	clustered := spec.hasFeature("clustered")
	if description.Clustered != clustered {
		return fmt.Errorf("effective CTDB mode does not match generated spec")
	}
	if description.DomainController {
		return fmt.Errorf("AD controller configuration is not supported; use an ADS member")
	}
	if spec.hasFeature("domain") && !clustered {
		return fmt.Errorf("Active Directory requires clustered SMB; use clustering: always")
	}
	if clustered {
		// Includes execute while parsing, even if a later option supersedes
		// them. A share include can also reopen globals after our overlay.
		// Inspect all selected options, not just the final global map.
		sections := [][][2]string{description.Globals}
		for _, share := range description.Shares {
			// Samba treats these strwicmp-equivalent names as global sections.
			// Sambacc renders them after our overlay, so they could replace
			// the admitted identity or CTDB policy. Leave legacy nonclustered
			// rendering unchanged, as with includes.
			name := smbPolicyOptionName(share.Name)
			if name == "global" || name == "globals" {
				return fmt.Errorf("clustered SMB share uses a reserved global section name")
			}
			sections = append(sections, share.Options)
		}
		for _, options := range sections {
			for _, option := range options {
				if smbPolicyOptionName(option[0]) == "include" {
					return fmt.Errorf("clustered SMB does not support include directives")
				}
			}
		}
	}
	// Sambacc preserves order; only the last Samba-equivalent option wins.
	effective := smbEffectiveGlobals(description)
	member := spec.hasFeature("domain")
	for _, key := range []string{"security", "realm", "serverrole", "clustering"} {
		value, present := effective[key]
		value = strings.ToLower(value)
		if !present {
			continue
		}
		switch key {
		case "security":
			security := smbPolicyOptionName(value)
			if (member && security != "ads") || (!member && security != "user" && security != "auto") {
				return fmt.Errorf("effective domain/security mode is not prepared")
			}
		case "realm":
			if !member && value != "" {
				return fmt.Errorf("effective realm is not prepared")
			}
		case "serverrole":
			// Samba compares enum values with the same strwicmp semantics as
			// parameter names. Apply this only to the role, not string options.
			role := smbPolicyOptionName(value)
			if (member && role != "auto" && role != "member" && role != "memberserver") || (!member && role != "auto" && role != "standalone" && role != "standaloneserver") {
				return fmt.Errorf("effective server role is not prepared")
			}
		case "clustering":
			// Samba set_boolean uses strwicmp, including on/off. Compare
			// only this Boolean value; never rewrite the rendered input.
			boolean := smbPolicyOptionName(value)
			on := boolean == "yes" || boolean == "true" || boolean == "on" || boolean == "1"
			off := boolean == "no" || boolean == "false" || boolean == "off" || boolean == "0"
			if (!on && !off) || on != clustered {
				return fmt.Errorf("effective clustering override conflicts with generated spec")
			}
		}
	}
	if member {
		if smbPolicyOptionName(effective["security"]) != "ads" {
			return fmt.Errorf("AD member requires effective ADS security")
		}
	}
	role := smbPolicyOptionName(effective["serverrole"])
	if !member && (role == "" || role == "auto") {
		// Pinned Samba lp_find_server_role derives a controller for USER/AUTO
		// with domain logons: domain master=yes/auto selects PDC, no selects
		// BDC. Neither is supported. Explicit standalone and ADS member roles
		// remain valid regardless of these flags. The default is no logons.
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

var inspectSMBPolicyFunc = inspectSMBPolicy

func inspectSMBPolicy(ctx context.Context, root string, candidate smbCandidate) (*smbADMember, error) {
	err := smbNativeRoot(root, candidate.Spec.ClusterID)
	if err != nil {
		return nil, err
	}
	document, err := smbDecodeObject(candidate.Config)
	if err != nil {
		return nil, err
	}
	ordered, err := smbOrderedBindingDocument(candidate.Config, document, nil)
	if err != nil {
		return nil, err
	}
	var description smbBindingDescription
	err = smbPythonBinding(ctx, root)("inspect", smbBindingRequest{ClusterID: candidate.Spec.ClusterID, Config: ordered}, &description)
	if err != nil {
		smbNativeFailure(root, &err)
		return nil, err
	}
	return smbMemberContext(candidate, description)
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
