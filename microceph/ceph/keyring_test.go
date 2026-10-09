package ceph

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
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
	r := withMockRunner(ks.T())
	ctx := context.Background()

	// mocks and expectations: a single get-or-create call, no print-key.
	r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.RemoteName",
		"mon", "allow *", "osd", "allow r", "--format", "json").
		Return(`[{"entity":"client.RemoteName","key":"ABCD","caps":{"mon":"allow *","osd":"allow r"}}]`, nil).Once()

	// Method call
	clientKey, err := CreateClientKey(ctx, "RemoteName", []string{"mon", "allow *"}, []string{"osd", "allow r"})

	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), "ABCD", clientKey)
}

// TestClientKeyringCreationPassesCephErrorThrough checks that a failing
// get-or-create is reported as it was before the key was read from its output.
func (ks *KeyringSuite) TestClientKeyringCreationPassesCephErrorThrough() {
	r := withMockRunner(ks.T())

	// get-or-create refuses to reuse a client whose capabilities differ from the requested ones.
	cephErr := errors.New("Failed to run: ceph auth get-or-create client.RemoteName mon allow * --format json: " +
		"exit status 22 (Error EINVAL: key for client.RemoteName exists but cap mon does not match)")
	// Output that comes with an error must not be mistaken for a key.
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName",
		"mon", "allow *", "--format", "json").
		Return(`[{"entity":"client.RemoteName","key":"ABCD"}]`, cephErr).Once()

	clientKey, err := CreateClientKey(context.Background(), "RemoteName", []string{"mon", "allow *"})

	assert.Equal(ks.T(), cephErr, err)
	assert.Empty(ks.T(), clientKey)
}

// A concurrent get-or-create may print only a blank line; ask again for its key
// without letting the second ceph process outlive the caller's context.
func (ks *KeyringSuite) TestClientKeyringCreationRetriesEmptyReply() {
	r := withMockRunner(ks.T())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
		Return("\n", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
		Run(func(args mock.Arguments) {
			cephCtx := args.Get(0).(context.Context)
			assert.NoError(ks.T(), cephCtx.Err())
			cancel()
			assert.ErrorIs(ks.T(), cephCtx.Err(), context.Canceled)
		}).
		Return("\n"+`[{"entity":"client.RemoteName","key":"ABCD"}]`, nil).Once()

	clientKey, err := CreateClientKey(ctx, "RemoteName")

	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), "ABCD", clientKey)
}

func (ks *KeyringSuite) TestClientKeyringCreationReportsRetryError() {
	r := withMockRunner(ks.T())
	cephErr := errors.New("ceph auth get-or-create failed")
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
		Return("\n", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
		Return("", cephErr).Once()

	clientKey, err := CreateClientKey(context.Background(), "RemoteName")

	assert.ErrorIs(ks.T(), err, cephErr)
	assert.Empty(ks.T(), clientKey)
}

func (ks *KeyringSuite) TestClientKeyringCreationStopsAfterTwoEmptyReplies() {
	r := withMockRunner(ks.T())
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
		Return("\n", nil).Twice()

	clientKey, err := CreateClientKey(context.Background(), "RemoteName")

	assert.ErrorContains(ks.T(), err, "ceph auth returned empty output for client.RemoteName after retry")
	assert.Empty(ks.T(), clientKey)
}

func (ks *KeyringSuite) TestClientKeyringCreationRejectsUnusableOutput() {
	cases := []struct {
		name    string
		output  string
		wantErr string
	}{
		{name: "plain text keyring", output: "[client.RemoteName]\n\tkey = ABCD\n", wantErr: "failed to parse the ceph auth output"},
		{
			name:    "only another entity",
			output:  `[{"entity":"client.other","key":"ABCD"}]`,
			wantErr: "ceph auth returned no entry for client.RemoteName",
		},
		{
			name:    "empty key",
			output:  `[{"entity":"client.RemoteName","key":""}]`,
			wantErr: "ceph auth returned an empty key for client.RemoteName",
		},
	}

	for _, tc := range cases {
		ks.Run(tc.name, func() {
			r := withMockRunner(ks.T())
			r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.RemoteName", "--format", "json").
				Return(tc.output, nil).Once()

			clientKey, err := CreateClientKey(context.Background(), "RemoteName")

			assert.ErrorContains(ks.T(), err, tc.wantErr)
			assert.Empty(ks.T(), clientKey)
		})
	}
}

func (ks *KeyringSuite) TestClientKeyringDelete() {
	r := mocks.NewRunner(ks.T())

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ceph", "auth", "del", "client.RemoteName"}...).Return("ok", nil).Once()
	common.ProcessExec = r

	// Method call
	err := DeleteClientKey("RemoteName")

	assert.NoError(ks.T(), err)
}
