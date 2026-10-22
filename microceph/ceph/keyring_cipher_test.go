package ceph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCreateKeyringsUsesCipher(t *testing.T) {
	r := mocks.NewRunner(t)
	original := common.ProcessExec
	common.ProcessExec = r
	t.Cleanup(func() { common.ProcessExec = original })

	confPath := t.TempDir()
	adminKeyring := filepath.Join(confPath, "ceph.client.admin.keyring")

	r.On("RunCommand", "ceph-authtool", "--create-keyring", mock.Anything,
		"--gen-key", "-n", "mon.", "--key-type", common.CipherAES256,
		"--cap", "mon", "allow *").Return("ok", nil).Once()
	r.On("RunCommand", "ceph-authtool", "--create-keyring", adminKeyring,
		"--gen-key", "-n", "client.admin", "--key-type", common.CipherAES256,
		"--cap", "mon", "allow *", "--cap", "osd", "allow *",
		"--cap", "mds", "allow *", "--cap", "mgr", "allow *").Return("ok", nil).Once()
	r.On("RunCommand", "ceph-authtool", mock.Anything, "--import-keyring", adminKeyring).Return("ok", nil).Once()

	path, err := CreateKeyrings(confPath, common.CipherAES256)
	assert.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(path) })
}

func TestGenMonmapUsesCipher(t *testing.T) {
	r := mocks.NewRunner(t)
	original := common.ProcessExec
	common.ProcessExec = r
	t.Cleanup(func() { common.ProcessExec = original })

	r.On("RunCommand", "monmaptool", "--create",
		"--auth-preferred-cipher", common.CipherAES,
		"--auth-allowed-ciphers", common.CipherAES,
		"--fsid", "test-fsid", "/tmp/mon.map").Return("ok", nil).Once()

	err := genMonmap("/tmp/mon.map", "test-fsid", common.CipherAES)
	assert.NoError(t, err)
}

