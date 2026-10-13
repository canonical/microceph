package ceph

import (
	"encoding/json"
	"fmt"
)

// combineSMBUserSources adapts manager-generated identity documents to sambacc's
// overwrite-based loader. Entry order and upstream ID allocation stay unchanged.
func combineSMBUserSources(sources [][]byte) ([][]byte, error) {
	if len(sources) < 2 {
		return sources, nil
	}
	combined := map[string]json.RawMessage{}
	entries := map[string][]json.RawMessage{"users": {}, "groups": {}}
	names := map[string]map[string]bool{"users": {}, "groups": {}}
	for index, data := range sources {
		var source map[string]json.RawMessage
		err := json.Unmarshal(data, &source)
		if err != nil || source == nil {
			return nil, fmt.Errorf("invalid SMB identity source %d", index)
		}
		var version string
		err = json.Unmarshal(source["samba-container-config"], &version)
		if err != nil || version != "v0" {
			return nil, fmt.Errorf("invalid SMB identity source %d version", index)
		}
		for key, value := range source {
			// Preserve other fields with the loader's existing last-source rule.
			if key != "users" && key != "groups" {
				combined[key] = value
			}
		}
		for _, kind := range []string{"users", "groups"} {
			value, ok := source[kind]
			if !ok {
				continue
			}
			var section map[string]json.RawMessage
			err = json.Unmarshal(value, &section)
			if err != nil || section == nil {
				return nil, fmt.Errorf("invalid SMB %s in identity source %d", kind, index)
			}
			var batch []json.RawMessage
			err = json.Unmarshal(section["all_entries"], &batch)
			if err != nil || batch == nil {
				return nil, fmt.Errorf("invalid SMB %s list in identity source %d", kind, index)
			}
			for _, entry := range batch {
				var identity struct {
					Name string `json:"name"`
				}
				err = json.Unmarshal(entry, &identity)
				if err != nil || identity.Name == "" {
					return nil, fmt.Errorf("invalid SMB %s entry in identity source %d", kind, index)
				}
				if names[kind][identity.Name] {
					return nil, fmt.Errorf("duplicate name in SMB %s sources", kind)
				}
				names[kind][identity.Name] = true
				entries[kind] = append(entries[kind], entry)
			}
			section["all_entries"], err = json.Marshal(entries[kind])
			if err != nil {
				return nil, fmt.Errorf("failed to encode combined SMB %s", kind)
			}
			combined[kind], err = json.Marshal(section)
			if err != nil {
				return nil, fmt.Errorf("failed to encode combined SMB %s configuration", kind)
			}
		}
	}
	data, err := json.Marshal(combined)
	if err != nil {
		return nil, fmt.Errorf("failed to encode combined SMB identity sources")
	}
	return [][]byte{data}, nil
}
