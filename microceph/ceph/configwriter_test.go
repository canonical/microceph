package ceph

import (
	"os"
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
