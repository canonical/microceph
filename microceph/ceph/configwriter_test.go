package ceph

import (
	"github.com/canonical/microceph/microceph/tests"
	"os"
	"testing"

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
	config := newCephConfig(s.Tmp)
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
func (s *configWriterSuite) TestWriteRadosGWConfig() {
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
func (s *configWriterSuite) TestWriteRadosGWSSLConfig() {
	config := newRadosGWConfig(s.Tmp)
	err := config.WriteConfig(
		map[string]any{
			"monitors":       "foohost",
			"rgwPort":        80,
			"sslPort":        443,
			"sslCertificate": "/var/snap/microceph/common/server.crt",
			"sslPrivateKey":  "/var/snap/microceph/common/server.key",
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
	assert.Contains(s.T(), string(data), "rgw frontends = beast port=80 ssl_port=443 ssl_certificate=/var/snap/microceph/common/server.crt ssl_private_key=/var/snap/microceph/common/server.key")
}

// Test ceph keyring writing
func (s *configWriterSuite) TestWriteCephKeyring() {
	keyring := newCephKeyring(s.Tmp, "ceph.keyring")
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
