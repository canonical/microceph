package common

import (
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

const (
	// CipherAES is the legacy AES-128 CephX key cipher.
	CipherAES = "aes"
	// CipherAES256 is the AES-256 CephX key cipher. Ceph calls this key type
	// "aes256k" (AES-256 with Kerberos-style derivation).
	CipherAES256 = "aes256k"
)

// NormalizeCipher converts a user-provided cipher name to the corresponding
// Ceph key type. An empty value means that the cipher was not specified and
// Ceph's default should be left untouched.
func NormalizeCipher(cipher string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(cipher)) {
	case "", "default", "auto":
		return "", nil
	case "aes", "aes128", "aes-128":
		return CipherAES, nil
	case "aes256", "aes-256", "aes256k", "aes-256k", "aes256krb5":
		return CipherAES256, nil
	default:
		return "", fmt.Errorf("unsupported default cipher %q: supported values are %q (AES) and %q (AES-256)", cipher, CipherAES, CipherAES256)
	}
}

// SupportsAES256 reports whether the installed Ceph tools can generate AES-256
// CephX keys. It probes ceph-authtool directly so that the result reflects the
// actual binaries in use rather than a release number that may have been
// backported from.
func SupportsAES256() (bool, error) {
	_, err := ProcessExec.RunCommand("ceph-authtool", "--gen-print-key", "--key-type", CipherAES256)
	if err != nil {
		return false, fmt.Errorf("failed to probe AES-256 CephX key support: %w", err)
	}

	return true, nil
}

// SupportsAES256Func is the injectable form of SupportsAES256.
var SupportsAES256Func = SupportsAES256

// ResolveDefaultCipher returns the effective CephX key cipher for a bootstrap.
//
// An explicit cipher is normalised and passed through. When no cipher is
// requested, AES-256 is selected if the installed Ceph tools support it;
// otherwise an empty string is returned so that Ceph's own defaults are left
// untouched. An empty string is also returned for an explicit AES request on a
// Ceph release too old to accept a key type, since those releases already
// default to AES.
func ResolveDefaultCipher(requested string) (string, error) {
	cipher, err := NormalizeCipher(requested)
	if err != nil {
		return "", err
	}

	if cipher == CipherAES256 {
		// An explicit AES-256 request is passed through: the operator asked for
		// it, and ceph-authtool reports a clear error if the installed Ceph is
		// too old.
		return cipher, nil
	}

	supported, supportErr := SupportsAES256Func()

	if cipher == "" {
		if supportErr != nil {
			logger.Debugf("Unable to determine AES-256 CephX key support, leaving Ceph defaults untouched: %v", supportErr)
			return "", nil
		}
		if supported {
			return CipherAES256, nil
		}
		return "", nil
	}

	if cipher == CipherAES {
		if supportErr != nil || !supported {
			// Ceph releases without AES-256 key support also lack the key-type
			// options used to force AES. They already default to AES, so leave
			// the defaults untouched.
			logger.Debugf("AES-256 CephX key support is unavailable, leaving the legacy AES default untouched")
			return "", nil
		}
		return cipher, nil
	}

	return cipher, nil
}

// GetDefaultCipher returns the CephX key cipher that should be used by default
// on this system. It returns an empty string when AES-256 is unsupported, which
// means Ceph's defaults should be left untouched.
func GetDefaultCipher() (string, error) {
	return ResolveDefaultCipher("")
}
