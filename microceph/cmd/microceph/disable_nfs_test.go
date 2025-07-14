package main

import (
	"strings"
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type disableNFSSuite struct {
	tests.BaseSuite
}

func TestDisableNFS(t *testing.T) {
	suite.Run(t, new(disableNFSSuite))
}

// Set up test suite
func (s *disableNFSSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *disableNFSSuite) TestCmdDisableNFSInvalid() {
	cmd := cmdDisableNFS{}
	err := cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a valid cluster ID using the `--cluster-id` flag")

	cmd.flagClusterID = ".foo"
	err = cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a valid cluster ID using the `--cluster-id` flag")

	cmd.flagClusterID = strings.Repeat("a", 64)
	err = cmd.Run(nil, []string{})
	assert.ErrorContains(s.T(), err, "please provide a valid cluster ID using the `--cluster-id` flag")
}
