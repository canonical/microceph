package ceph

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type configWriterSuite struct {
	tests.BaseSuite
}

func TestConfigWriter(t *testing.T) {
	suite.Run(t, new(configWriterSuite))
}

// Set up test suite
func (s *configWriterSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

// Test ceph config writing
func (s *configWriterSuite) TestWriteCephConfig() {

	track := constants.GetPathConst
	defer func() { constants.GetPathConst = track }()

	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{
			ConfPath: s.Tmp,
		}
	}

	config := NewCephConfig(constants.CephConfFileName)
	err := config.WriteConfig(
		map[string]any{
			"fsid":     "fsid1234",
			"runDir":   "/tmp/somedir",
			"monitors": "foohost",
			"addr":     "foohost",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "fsid = fsid1234")
}

// Test ceph config writing
func (s *configWriterSuite) TestWriteRadosGWNonSSLConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors": "foohost",
			"rgwPort":  80,
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "foohost")
	assert.Contains(s.T(), string(data), "rgw frontends = beast port=80\n")
}

// Test ceph config writing
func (s *configWriterSuite) TestWriteRadosGWCompleteConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors":           "foohost",
			"rgwPort":            80,
			"sslPort":            443,
			"sslCertificatePath": "/tmp/server.crt",
			"sslPrivateKeyPath":  "/tmp/server.key",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "foohost")
	assert.Contains(s.T(), string(data), "rgw frontends = beast port=80 ssl_port=443 ssl_certificate=/tmp/server.crt ssl_private_key=/tmp/server.key")
}

func (s *configWriterSuite) TestWriteRadosGWSSLOnlyConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors":           "foohost",
			"rgwPort":            0,
			"sslPort":            443,
			"sslCertificatePath": "/tmp/server.crt",
			"sslPrivateKeyPath":  "/tmp/server.key",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "foohost")
	assert.Contains(s.T(), string(data), "rgw frontends = beast ssl_port=443 ssl_certificate=/tmp/server.crt ssl_private_key=/tmp/server.key")
}

func (s *configWriterSuite) TestWriteRadosGWWithMissingSSLCertificateConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors":           "foohost",
			"rgwPort":            80,
			"sslPort":            443,
			"sslCertificatePath": "",
			"sslPrivateKeyPath":  "/tmp/server.key",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "foohost")
	assert.Contains(s.T(), string(data), "rgw frontends = beast port=80\n")
}

func (s *configWriterSuite) TestWriteRadosGWWithMissingSSLPrivateKeyConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors":           "foohost",
			"rgwPort":            80,
			"sslPort":            443,
			"sslCertificatePath": "/tmp/server.crt",
			"sslPrivateKeyPath":  "",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "foohost")
	assert.Contains(s.T(), string(data), "rgw frontends = beast port=80\n")
}

// Test ceph keyring writing
func (s *configWriterSuite) TestWriteCephKeyring() {
	keyring := NewCephKeyring(s.Tmp, "ceph.keyring")
	err := keyring.WriteConfig(
		map[string]any{
			"name": "client.admin",
			"key":  "secretkey",
		},
		0644,
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists and has the right contents
	_, err = os.Stat(keyring.GetPath())
	assert.Equal(s.T(), nil, err)
	data, err := os.ReadFile(keyring.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "key = secretkey")
}

func (s *configWriterSuite) TestWriteConfigRejectsNonLocalFilenames() {
	tests := []string{
		"../escaped.keyring",
		"/tmp/escaped.keyring",
		"subdir/escaped.keyring",
	}

	for _, configFile := range tests {
		s.T().Run(configFile, func(t *testing.T) {
			keyring := NewCephKeyring(s.Tmp, configFile)
			err := keyring.WriteConfig(
				map[string]any{
					"name": "client.admin",
					"key":  "secretkey",
				},
				0644,
			)
			assert.Error(t, err)
		})
	}

	_, err := os.Stat(filepath.Join(s.Tmp, "..", "escaped.keyring"))
	assert.True(s.T(), os.IsNotExist(err))
}

// Test NFS Ganesha config writing
func (s *configWriterSuite) TestWriteGaneshaConfig() {
	config := newGaneshaConfig(s.Tmp)

	err := config.WriteConfig(
		map[string]any{
			"bindAddr":      "10.20.30.40",
			"bindPort":      "9999",
			"userID":        "foo",
			"clusterID":     "lish",
			"snapDir":       "/bar",
			"runDir":        "/tender",
			"confDir":       "/foo/lish",
			"minorVersions": 2,
		},
		0644,
	)

	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)

	dataStr := string(data)
	assert.Contains(s.T(), dataStr, "Bind_Addr = 10.20.30.40;")
	assert.Contains(s.T(), dataStr, "NFS_Port = 9999;")
	assert.Contains(s.T(), dataStr, "Minor_Versions = 2;")
	assert.Contains(s.T(), dataStr, "Plugins_Dir = \"/bar/lib/x86_64-linux-gnu/ganesha\";")
	assert.Contains(s.T(), dataStr, "CCacheDir = \"/tender/ganesha\";")
	assert.Contains(s.T(), dataStr, "UserId = \"foo\";")
	assert.Contains(s.T(), dataStr, "namespace = \"lish\";")
	assert.Contains(s.T(), dataStr, "ceph_conf = \"/foo/lish/ceph.conf\";")
	assert.Contains(s.T(), dataStr, "watch_url = \"rados://.nfs/lish/conf-nfs.lish\";")
	assert.Contains(s.T(), dataStr, "url = \"rados://.nfs/lish/conf-nfs.lish\";")
}

// Test ceph config writing for NFS Ganesha
func (s *configWriterSuite) TestWriteGaneshaCephConfig() {
	config := newGaneshaCephConfig(s.Tmp)

	err := config.WriteConfig(
		map[string]any{
			"monitors": "foo",
			"confDir":  "/foo/lish",
		},
		0644,
	)

	assert.Equal(s.T(), nil, err)
	// Check that the file exists
	_, err = os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	// Check contents of the file
	data, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "mon host = foo")
	assert.Contains(s.T(), string(data), "keyring = /foo/lish/keyring")
}

// TestWriteConfigConcurrentNoRace verifies that concurrent WriteConfig calls
// targeting the SAME destination file do not collide on a shared temp path:
// every writer creates its own uniquely-named temp file (os.CreateTemp) so no
// writer can rename a partially-written file from another writer into place.
// After all writers finish the destination must exist, hold a complete file
// (the expected marker is present), and no leftover temp files may remain in
// the config directory (every temp is either renamed away or removed).
func (s *configWriterSuite) TestWriteConfigConcurrentNoRace() {
	config := newRadosGWConfig(s.Tmp)
	data := map[string]any{
		"monitors": "foohost",
		"rgwPort":  80,
	}

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			err := config.WriteConfig(data, 0644)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Empty(s.T(), errs, "no concurrent WriteConfig call must return an error")

	// The destination file must exist, hold a complete render, and have the
	// requested mode (os.CreateTemp uses 0600; WriteConfig must os.Chmod it back).
	info, err := os.Stat(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Equal(s.T(), os.FileMode(0644), info.Mode().Perm(),
		"final file must have the requested mode, not the os.CreateTemp default 0600")
	contents, err := os.ReadFile(config.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(contents), "foohost",
		"final file must contain a complete render, not a torn write")

	// No leftover temp files: every temp file is either renamed into place or
	// removed on an error path. os.CreateTemp names match "<base>.*".
	entries, err := os.ReadDir(s.Tmp)
	assert.Equal(s.T(), nil, err)
	base := filepath.Base(config.GetPath())
	for _, e := range entries {
		name := e.Name()
		if name == base {
			continue
		}
		assert.False(s.T(), len(name) > len(base) && name[:len(base)+1] == base+".",
			"leftover temp file must not remain: %s", name)
	}
}
