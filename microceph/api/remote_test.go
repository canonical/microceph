package api

import (
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/stretchr/testify/assert"
)

func TestValidateRemoteNameRejectsTraversalAndReservedNames(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "../state"},
		{name: ".."},
		{name: "remote/name"},
		{name: "remote-name"},
		{name: ""},
		{name: "ceph"},
		{name: "ganesha"},
		{name: "radosgw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteName(tt.name)
			assert.Error(t, err)
		})
	}
}

func TestValidateRemoteNameAcceptsClusterNames(t *testing.T) {
	validNames := []string{
		"remote1",
		"a",
		"abc123",
		"123",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64 chars - should fail via regex
	}
	valid := validNames[:4]
	tooLong := validNames[4]

	for _, name := range valid {
		err := validateRemoteName(name)
		assert.NoError(t, err, "expected %q to be valid", name)
	}

	err := validateRemoteName(tooLong)
	assert.Error(t, err, "expected 64-char name to be rejected")
}

func TestValidateRemoteImportRequest(t *testing.T) {
	req := types.RemoteImportRequest{
		Name:      "remote1",
		LocalName: "local1",
	}

	err := validateRemoteImportRequest("remote1", req)
	assert.NoError(t, err)

	err = validateRemoteImportRequest("other", req)
	assert.Error(t, err)

	req.LocalName = "../local"
	err = validateRemoteImportRequest("remote1", req)
	assert.Error(t, err)
}
