package common

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeCipher(t *testing.T) {
	cases := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{input: "", expected: ""},
		{input: "default", expected: ""},
		{input: "aes", expected: CipherAES},
		{input: "AES", expected: CipherAES},
		{input: "aes128", expected: CipherAES},
		{input: "aes256", expected: CipherAES256},
		{input: "AES-256", expected: CipherAES256},
		{input: "aes256k", expected: CipherAES256},
		{input: "AES256K", expected: CipherAES256},
		{input: "invalid", wantErr: true},
	}

	for _, tc := range cases {
		cipher, err := NormalizeCipher(tc.input)
		if tc.wantErr {
			assert.Error(t, err, "input %q", tc.input)
			continue
		}
		assert.NoError(t, err, "input %q", tc.input)
		assert.Equal(t, tc.expected, cipher, "input %q", tc.input)
	}
}

func TestBootstrapConfigDefaultCipherRoundTrip(t *testing.T) {
	original := BootstrapConfig{DefaultCipher: CipherAES256}

	encoded := EncodeBootstrapConfig(original)
	var decoded BootstrapConfig
	DecodeBootstrapConfig(encoded, &decoded)

	assert.Equal(t, original.DefaultCipher, decoded.DefaultCipher)
}

func withAES256Support(t *testing.T, supported bool, supportErr error) {
	t.Helper()

	original := SupportsAES256Func
	SupportsAES256Func = func() (bool, error) {
		return supported, supportErr
	}
	t.Cleanup(func() { SupportsAES256Func = original })
}

func TestResolveDefaultCipherAuto(t *testing.T) {
	withAES256Support(t, true, nil)

	cipher, err := ResolveDefaultCipher("")
	assert.NoError(t, err)
	assert.Equal(t, CipherAES256, cipher)

	withAES256Support(t, false, nil)

	cipher, err = ResolveDefaultCipher("")
	assert.NoError(t, err)
	assert.Empty(t, cipher, "unsupported AES-256 must leave Ceph defaults untouched")
}

func TestResolveDefaultCipherExplicit(t *testing.T) {
	withAES256Support(t, true, nil)

	cipher, err := ResolveDefaultCipher("aes")
	assert.NoError(t, err)
	assert.Equal(t, CipherAES, cipher)

	cipher, err = ResolveDefaultCipher("AES-256")
	assert.NoError(t, err)
	assert.Equal(t, CipherAES256, cipher)

	_, err = ResolveDefaultCipher("rot13")
	assert.ErrorContains(t, err, "unsupported default cipher")
}

func TestResolveDefaultCipherAESUnsupportedTools(t *testing.T) {
	withAES256Support(t, false, nil)

	cipher, err := ResolveDefaultCipher("aes")
	assert.NoError(t, err)
	assert.Empty(t, cipher, "legacy Ceph already defaults to AES, so no flags should be passed")

	cipher, err = ResolveDefaultCipher("aes256k")
	assert.NoError(t, err)
	assert.Equal(t, CipherAES256, cipher, "an explicit request is passed through")
}

func TestResolveDefaultCipherSupportProbeFailure(t *testing.T) {
	withAES256Support(t, false, errors.New("ceph-authtool not found"))

	cipher, err := ResolveDefaultCipher("")
	assert.NoError(t, err)
	assert.Empty(t, cipher)

	cipher, err = ResolveDefaultCipher("aes256k")
	assert.NoError(t, err)
	assert.Equal(t, CipherAES256, cipher, "an explicit request is passed through")
}
