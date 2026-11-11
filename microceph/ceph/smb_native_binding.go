package ceph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
)

// smbOrderedBindingDocument preserves the upstream order of option dictionaries.
// Go maps otherwise sort JSON keys, which can move directives around includes.
// For rendering, controlled options remain after upstream options, matching the
// former adapter. This is serialization order, not a second share generator.
func smbOrderedBindingDocument(original json.RawMessage, document map[string]any, controlledGlobals map[string]any) (map[string]any, error) {
	var source map[string]json.RawMessage
	err := json.Unmarshal(original, &source)
	if err != nil {
		return nil, err
	}
	result := maps.Clone(document)
	for _, kind := range []string{"globals", "shares"} {
		sections, ok := document[kind].(map[string]any)
		if !ok {
			continue
		}
		var originals map[string]struct {
			Options json.RawMessage `json:"options"`
		}
		if len(source[kind]) > 0 {
			err = json.Unmarshal(source[kind], &originals)
			if err != nil {
				return nil, err
			}
		}
		view := maps.Clone(sections)
		result[kind] = view
		for name, value := range sections {
			section, ok := value.(map[string]any)
			if !ok {
				continue
			}
			options, ok := section["options"].(map[string]any)
			if !ok {
				continue
			}
			keys, err := smbOptionKeys(originals[name].Options)
			if err != nil {
				return nil, err
			}
			controlled := func(key string) bool {
				if controlledGlobals == nil {
					return false
				}
				if kind == "shares" {
					return smbControlledShareOption(smbOptionName(key))
				}
				_, exists := controlledGlobals[smbOptionName(key)]
				return exists
			}
			order := make([]string, 0, len(options))
			seen := map[string]bool{}
			appendKey := func(key string) {
				_, exists := options[key]
				if exists && !seen[key] {
					order = append(order, key)
					seen[key] = true
				}
			}
			for _, key := range keys {
				if !controlled(key) {
					appendKey(key)
				}
			}
			// New uncontrolled keys are deterministic; native code normally adds only
			// controlled keys, while original arbitrary option ordering stays intact.
			extra := []string{}
			for key := range options {
				if !seen[key] && !controlled(key) {
					extra = append(extra, key)
				}
			}
			sort.Strings(extra)
			for _, key := range extra {
				appendKey(key)
			}
			for _, key := range keys {
				if controlled(key) {
					appendKey(smbOptionName(key))
				}
			}
			extra = nil
			for key := range options {
				if !seen[key] {
					extra = append(extra, key)
				}
			}
			sort.Strings(extra)
			for _, key := range extra {
				appendKey(key)
			}
			var encoded bytes.Buffer
			encoded.WriteByte('{')
			for i, key := range order {
				if i > 0 {
					encoded.WriteByte(',')
				}
				rawKey, err := json.Marshal(key)
				if err != nil {
					return nil, err
				}
				rawValue, err := json.Marshal(options[key])
				if err != nil {
					return nil, err
				}
				encoded.Write(rawKey)
				encoded.WriteByte(':')
				encoded.Write(rawValue)
			}
			encoded.WriteByte('}')
			copied := maps.Clone(section)
			copied["options"] = json.RawMessage(encoded.Bytes())
			view[name] = copied
		}
	}
	return result, nil
}

func smbOptionKeys(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("invalid upstream option object")
	}
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("invalid upstream option name")
		}
		var value json.RawMessage
		err = decoder.Decode(&value)
		if err != nil {
			return nil, err
		}
		keys = append(keys, name)
	}
	return keys, nil
}
