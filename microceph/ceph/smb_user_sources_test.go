package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSMBUserSourcesCombineInDeclaredOrder(t *testing.T) {
	_, conf, runtime := smbTestPaths(t, false)
	placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
	sources := map[string][]byte{placement.ConfigURI: []byte(smbTestContainer)}
	users, groups := []string{}, []string{}
	// Twelve references catch accidental users-10/users-2 filename ordering.
	for i := range 12 {
		uri := fmt.Sprintf("rados:mon-config-key:smb/config/files/users-groups.%d.json", i)
		placement.UserSources = append(placement.UserSources, uri)
		user := fmt.Sprintf(`{"name":"user-%02d","password":"fixture-only","gid":%d}`, i, 5000+i)
		if i == 0 {
			user = `{"name":"user-00","password":"fixture-only","uid":4242,"gid":5000,"future_number":9007199254740993}`
		}
		// A user and its group may have the same name; their namespaces differ.
		group := fmt.Sprintf(`{"name":"user-%02d","gid":%d}`, i, 5000+i)
		users = append(users, user)
		groups = append(groups, group)
		sources[uri] = []byte(fmt.Sprintf(`{"samba-container-config":"v0","users":{"all_entries":[%s]},"groups":{"all_entries":[%s]},"future":"retained"}`, user, group))
	}
	emptyURI := "rados:mon-config-key:smb/config/files/users-groups.12.json"
	placement.UserSources = append(placement.UserSources, emptyURI)
	sources[emptyURI] = []byte(`{"samba-container-config":"v0","users":{"all_entries":[]},"groups":{"all_entries":[]}}`)
	preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
	fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) { return sources[uri], nil }
	data, err := fetchSMBConfigSources(context.Background(), placement)
	require.NoError(t, err)
	require.Equal(t, 1, len(data.users))
	want := fmt.Sprintf(`{"samba-container-config":"v0","users":{"all_entries":[%s]},"groups":{"all_entries":[%s]},"future":"retained"}`, strings.Join(users, ","), strings.Join(groups, ","))
	assert.JSONEq(t, want, string(data.users[0]))
	assert.Contains(t, string(data.users[0]), "9007199254740993")
	assert.Equal(t, smbTestContainer, string(data.container))
	placement.configData = data
	smbTestWrite(t, filepath.Join(runtime, "users-1.json"), "obsolete separate source")
	require.NoError(t, materializeSMBConfig(context.Background(), placement))
	assert.Equal(t, string(data.users[0]), readSMBConfigFile(t, filepath.Join(runtime, "users-0.json")))
	assert.NoFileExists(t, filepath.Join(runtime, "users-1.json"))
	smbTestWrite(t, filepath.Join(conf, "ceph.client.smb.fs.cluster.files.keyring"), "fixture-key")
	assert.True(t, placement.hasLocalConfig())
	before, err := placement.effectiveConfigDigest()
	require.NoError(t, err)
	placement.configData, err = fetchSMBConfigSources(context.Background(), placement)
	require.NoError(t, err)
	after, err := placement.effectiveConfigDigest()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.True(t, placement.hasLocalConfig(), "combined files must satisfy the existing no-op check")
}

func TestSMBUserSourcesRejectAmbiguityBeforePublication(t *testing.T) {
	for _, tc := range []struct{ name, first, second, message string }{
		{"duplicate user", `{"samba-container-config":"v0","users":{"all_entries":[{"name":"alice","password":"private-one"}]}}`, `{"samba-container-config":"v0","users":{"all_entries":[{"name":"alice","password":"private-two"}]}}`, "duplicate name"},
		{"duplicate group", `{"samba-container-config":"v0","groups":{"all_entries":[{"name":"staff","gid":1000}]}}`, `{"samba-container-config":"v0","groups":{"all_entries":[{"name":"staff","gid":2000}]}}`, "duplicate name"},
		{"invalid JSON", `{`, `{}`, "invalid SMB identity source"},
		{"invalid version", `{"samba-container-config":"v1"}`, `{}`, "invalid SMB identity source"},
		{"invalid list", `{"samba-container-config":"v0","users":{"all_entries":{}}}`, `{}`, "invalid SMB users"},
		{"missing name", `{"samba-container-config":"v0","users":{"all_entries":[{}]}}`, `{}`, "invalid SMB users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, runtime := smbTestPaths(t, false)
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", UserSources: []string{
				"rados:mon-config-key:smb/config/files/users-groups.0.json",
				"rados:mon-config-key:smb/config/files/users-groups.1.json",
			}}
			preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
			fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) {
				switch uri {
				case placement.ConfigURI:
					return []byte(smbTestContainer), nil
				case placement.UserSources[0]:
					return []byte(tc.first), nil
				default:
					return []byte(tc.second), nil
				}
			}
			smbTestWrite(t, filepath.Join(runtime, "container.json"), "previous container")
			err := materializeSMBConfig(context.Background(), placement)
			require.ErrorContains(t, err, tc.message)
			assert.NotContains(t, err.Error(), "private-one")
			assert.NotContains(t, err.Error(), "private-two")
			assert.Equal(t, "previous container", readSMBConfigFile(t, filepath.Join(runtime, "container.json")))
			assert.NoFileExists(t, filepath.Join(runtime, "users-0.json"))
		})
	}
}

func TestSMBUserSourcesSingleInputRemainsByteExact(t *testing.T) {
	placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
	const uri = "rados:mon-config-key:smb/config/files/users-groups.0.json"
	const source = "{ \"samba-container-config\": \"v0\", \"users\": {\"all_entries\": []} }\n"
	preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
	fetchSMBSourceFunc = func(_ context.Context, path string) ([]byte, error) {
		if path == uri {
			return []byte(source), nil
		}
		return []byte(smbTestContainer), nil
	}
	for _, refs := range [][]string{nil, {uri}} {
		placement.UserSources = refs
		data, err := fetchSMBConfigSources(context.Background(), placement)
		require.NoError(t, err)
		require.Len(t, data.users, len(refs))
		if len(refs) > 0 {
			assert.Equal(t, source, string(data.users[0]))
			assert.True(t, json.Valid(data.users[0]))
		}
	}
}
