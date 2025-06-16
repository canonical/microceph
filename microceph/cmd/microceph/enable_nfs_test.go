package main

import (
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type enableNFSSuite struct {
	tests.BaseSuite
}

func TestEnableNFS(t *testing.T) {
	suite.Run(t, new(enableNFSSuite))
}

// Set up test suite
func (s *enableNFSSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *enableNFSSuite) TestCmdEnableNFSEmpty() {
	cmd := cmdEnableNFS{}
	err := cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a cluster ID using the `--cluster-id` flag")
}

func (s *enableNFSSuite) TestCmdEnableNFSInvalidV4() {
	cmd := cmdEnableNFS{
		flagClusterID:    "foo",
		flagV4MinVersion: 3,
	}

	err := cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a valid v4 minimum version (0, 1, 2) using the `--v4-min-version` flag")
}

func (s *enableNFSSuite) TestCmdEnableNFSInvalidAddress() {
	cmd := cmdEnableNFS{
		flagClusterID: "foo",
		flagBindAddr:  "10.20.30",
	}

	err := cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "could not parse the given `--bind-address`")
}

func (s *enableNFSSuite) TestCmdEnableNFSInvalidPort() {
	cmd := cmdEnableNFS{
		flagClusterID: "foo",
		flagBindAddr:  "0.0.0.0",
		flagBindPort:  0,
	}

	err := cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a valid port number [1, 49151] using the `--bind-port` flag")
}
