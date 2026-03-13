package ceph

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/canonical/microceph/microceph/common"

	"github.com/canonical/microceph/microceph/api/types"

	"github.com/tidwall/gjson"
)

// IsValidCrushName checks whether a name is a valid CRUSH bucket name.
// Matches the validation in Ceph's CrushWrapper::is_valid_crush_name.
// https://github.com/ceph/ceph/blob/main/src/crush/CrushWrapper.cc
func IsValidCrushName(name string) bool {
	const pattern = `^[a-zA-Z0-9_.\-]+$`
	return regexp.MustCompile(pattern).MatchString(name)
}

// ##### Public Methods #####

// BootstrapCrushRules sets up and configures the crush rules for failure domain handling.
func BootstrapCrushRules() error {
	// setup up crush rules
	err := ensureCrushRules()
	if err != nil {
		return err
	}

	// configure the default crush rule for new pools
	err = setDefaultCrushRule("microceph_auto_osd")
	if err != nil {
		return err
	}

	return nil
}

// addCrushRule creates a new default crush rule with a given name and failure domain
func addCrushRule(name string, failureDomain string) error {
	_, err := common.ProcessExec.RunCommand("ceph", "osd", "crush", "rule", "create-replicated", name, "default", failureDomain)
	if err != nil {
		return err
	}

	return nil
}

// listCrushRules returns a list of crush rule names
func listCrushRules() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "osd", "crush", "rule", "ls")
	if err != nil {
		return nil, err
	}
	rules := strings.Split(strings.TrimSpace(output), "\n")
	return rules, nil
}

// haveCrushRule returns true if a crush rule with the given name exists
func haveCrushRule(name string) bool {
	rules, err := listCrushRules()
	if err != nil {
		return false
	}
	for _, rule := range rules {
		if rule == name {
			return true
		}
	}
	return false
}

// getCrushRuleID returns the id of a crush rule with the given name
func getCrushRuleID(name string) (string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "osd", "crush", "rule", "dump", name)
	if err != nil {
		return "", err
	}
	var jsond map[string]any
	err = json.Unmarshal([]byte(output), &jsond)
	if err != nil {
		return "", err
	}
	val, ok := jsond["rule_id"]
	if !ok {
		return "", fmt.Errorf("rule_id not found in crush rule dump")
	}
	return fmt.Sprintf("%v", val), nil // convert to string
}

// getPoolsForDomain returns a list of pools that use a given crush failure domain
func getPoolsForDomain(domain string) ([]string, error) {
	var pools []string

	// check if the crush rule exists and bail if not
	if !haveCrushRule(fmt.Sprintf("microceph_auto_%s", domain)) {
		// nothing to do, bail
		return pools, nil
	}

	ruleID, err := getCrushRuleID(fmt.Sprintf("microceph_auto_%s", domain))
	if err != nil {
		return nil, err
	}

	output, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "ls", "detail", "--format=json")
	if err != nil {
		return nil, err
	}
	poolobjs := gjson.Get(output, fmt.Sprintf("#(crush_rule==%s)#.pool_name", ruleID))
	for _, poolobj := range poolobjs.Array() {
		pools = append(pools, poolobj.String())
	}
	return pools, nil
}

// setPoolCrushRule sets the crush rule for a given pool
func setPoolCrushRule(pool string, rule string) error {
	_, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "set", pool, "crush_rule", rule)
	if err != nil {
		return err
	}
	return nil
}

// setDefaultCrushRule sets the default crush rule for new pools
func setDefaultCrushRule(rule string) error {
	rid, err := getCrushRuleID(rule)
	if err != nil {
		return err
	}
	err = SetConfigItem(types.Config{
		Key:   "osd_pool_default_crush_rule",
		Value: rid,
	})
	if err != nil {
		return err
	}
	return nil
}

// getDefaultCrushRule returns the current default crush rule for new pools
func getDefaultCrushRule() (string, error) {
	configs, err := GetConfigItem(types.Config{
		Key: "osd_pool_default_crush_rule",
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(configs[0].Value), nil
}

// countAZsWithOSDs returns the number of AZ rack buckets that contain at least one OSD.
// It queries the CRUSH tree and checks each AZ rack for child hosts that have OSDs.
// The currentAZ parameter is the AZ of the host currently adding an OSD — it is always
// counted as active since the OSD may not yet be visible in the CRUSH tree.
// Note: there is technically a race between this check and the caller switching the
// CRUSH rule — an OSD could be removed in between. We accept this because the same
// race exists after the rule is active (an OSD removal can always cause degraded PGs).
func countAZsWithOSDs(azNames map[string]bool, currentAZ string) (int, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "osd", "tree", "-f", "json")
	if err != nil {
		return 0, fmt.Errorf("failed to get osd tree: %w", err)
	}

	nodes := gjson.Get(output, "nodes")
	count := 0
	for az := range azNames {
		// The current host's AZ is always active — we're adding an OSD to it right now.
		if az == currentAZ {
			count++
			continue
		}
		// Find the rack node for this AZ and get its children (host IDs).
		// Use the "az." prefix and filter by type=="rack" to avoid matching
		// a host or other bucket that happens to share the same name.
		rackBucket := fmt.Sprintf("az.%s", az)
		rackNode := nodes.Get(fmt.Sprintf(`#(name=="%s")`, rackBucket))
		if !rackNode.Exists() || rackNode.Get("type").String() != "rack" {
			continue
		}
		children := rackNode.Get("children")
		if !children.Exists() {
			continue
		}
		// Check if any child host has OSDs (children with id >= 0).
		hasOSD := false
		for _, hostID := range children.Array() {
			hostChildren := nodes.Get(fmt.Sprintf(`#(id==%d).children`, hostID.Int()))
			for _, osdID := range hostChildren.Array() {
				if osdID.Int() >= 0 {
					hasOSD = true
					break
				}
			}
			if hasOSD {
				break
			}
		}
		if hasOSD {
			count++
		}
	}
	return count, nil
}

// ensureCrushRules set up the crush rules for the automatic failure domain handling.
func ensureCrushRules() error {
	// Add a microceph default rule with failure domain OSD if it does not exist.
	if !haveCrushRule("microceph_auto_osd") {
		err := addCrushRule("microceph_auto_osd", "osd")
		if err != nil {
			return fmt.Errorf("Failed to add microceph default crush rule: %w", err)
		}
	}
	// Add a microceph default rule with failure domain host if it does not exist.
	if !haveCrushRule("microceph_auto_host") {
		err := addCrushRule("microceph_auto_host", "host")
		if err != nil {
			return fmt.Errorf("Failed to add microceph default crush rule: %w", err)
		}
	}
	// Add a microceph default rule with failure domain rack if it does not exist.
	if !haveCrushRule("microceph_auto_rack") {
		err := addCrushRule("microceph_auto_rack", "rack")
		if err != nil {
			return fmt.Errorf("Failed to add microceph default crush rule: %w", err)
		}
	}
	return nil
}
