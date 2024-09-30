package ceph

import (
	"testing"

	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type KeyringSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestKeyring(t *testing.T) {
	suite.Run(t, new(KeyringSuite))
}

func (ks *KeyringSuite) SetupTest() {
	ks.BaseSuite.SetupTest()
	ks.CopyCephConfigs()
}

func (ks *KeyringSuite) TestClientKeyringCreation() {
	r := mocks.NewRunner(ks.T())

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ceph", "auth", "get-or-create", "client.RemoteName"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"ceph", "auth", "print-key", "client.RemoteName"}...).Return("ABCD", nil).Once()
	processExec = r

	// Method call
	clientKey, err := CreateClientKey("RemoteName")

	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), clientKey, "ABCD")
}

func (ks *KeyringSuite) TestClientKeyringDelete() {
	r := mocks.NewRunner(ks.T())

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ceph", "auth", "del", "client.RemoteName"}...).Return("ok", nil).Once()
	processExec = r

	// Method call
	err := DeleteClientKey("RemoteName")

	assert.NoError(ks.T(), err)
}
