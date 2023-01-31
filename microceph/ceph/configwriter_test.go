package ceph

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"os"
	"testing"
)

type configWriterSuite struct {
	suite.Suite
	tmp string
}

func TestConfigWriter(t *testing.T) {
	suite.Run(t, new(configWriterSuite))
}

// Set up test suite
func (s *configWriterSuite) SetupTest() {
	tmp, err := os.MkdirTemp("", "microceph-test")
	if err != nil {
		s.T().Fatal("error creating tmp:", err)
	}

	s.tmp = tmp
}

func (s *configWriterSuite) TearDownTest() {
	os.RemoveAll(s.tmp)
}

// Test ceph config writing
func (s *configWriterSuite) TestWriteCephConfig() {
	config := newCephConfig(s.tmp)
	err := config.WriteConfig(
		map[string]any{
			"fsid":     "fsid1234",
			"runDir":   "/tmp/somedir",
			"monitors": "foohost",
			"addr":     "foohost",
		},
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

// Test ceph keyring writing
func (s *configWriterSuite) TestWriteCephKeyring() {
	keyring := newCephKeyring(s.tmp)
	err := keyring.WriteConfig(
		map[string]any{
			"name": "client.admin",
			"key":  "secretkey",
		},
	)
	assert.Equal(s.T(), nil, err)
	// Check that the file exists and has the right contents
	_, err = os.Stat(keyring.GetPath())
	assert.Equal(s.T(), nil, err)
	data, err := os.ReadFile(keyring.GetPath())
	assert.Equal(s.T(), nil, err)
	assert.Contains(s.T(), string(data), "key = secretkey")
}
