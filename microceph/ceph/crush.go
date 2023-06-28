package ceph

import (
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
)

// removeCrushRule removes a named crush rule
func removeCrushRule(name string) error {
	_, err := processExec.RunCommand("ceph", "osd", "crush", "rule", "rm", name)
	if err != nil {
		return err
	}

	return nil
}

// addCrushRule creates a new default crush rule with a given name and failure domain
func addCrushRule(name string, failureDomain string) error {
	_, err := processExec.RunCommand("ceph", "osd", "crush", "rule", "create-replicated", name, "default", failureDomain)
	if err != nil {
		return err
	}

	return nil
}

// listCrushRules returns a list of crush rule names
func listCrushRules() ([]string, error) {
	output, err := processExec.RunCommand("ceph", "osd", "crush", "rule", "ls")
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
	output, err := processExec.RunCommand("ceph", "osd", "crush", "rule", "dump", name)
	if err != nil {
		return "", err
	}
	var jsond map[string]any
	err = json.Unmarshal([]byte(output), &jsond)
	val, ok := jsond["rule_id"]
	if !ok {
		return "", fmt.Errorf("rule_id not found in crush rule dump")
	}
	return fmt.Sprintf("%v", val), nil // convert to string
}

// getPoolsForDomain returns a list of pools that use a given crush failure domain
func getPoolsForDomain(domain string) ([]string, error) {
	var pools []string

	ruleID, err := getCrushRuleID(fmt.Sprintf("microceph_auto_%s", domain))
	if err != nil {
		return nil, err
	}

	output, err := processExec.RunCommand("ceph", "osd", "pool", "ls", "detail", "--format=json")
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
	_, err := processExec.RunCommand("ceph", "osd", "pool", "set", pool, "crush_rule", rule)
	if err != nil {
		return err
	}
	return nil
}
