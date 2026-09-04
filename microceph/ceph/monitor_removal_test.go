package ceph

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEnsureMonAbsentAlreadyAbsent(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-b"}]}`, nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMonAbsentRefusesWithoutAnotherQuorumMember(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"}]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum":[{"rank":0,"name":"node-a"}]}`, nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.ErrorIs(t, err, ErrKeepOneInvariant)
}

func TestEnsureMonAbsentRefusesDegradedResultingMonmap(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"},{"name":"node-b"},{"name":"node-c"}]}`, nil).Once()
	// node-a and node-b form the current two-of-three quorum. Removing node-a
	// would produce a two-MON map where node-b alone cannot form quorum.
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum_names":["node-a","node-b"]}`, nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.ErrorIs(t, err, ErrKeepOneInvariant)
}

func TestEnsureMonAbsentTrustsCommittedRemovalReply(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"},{"name":"node-b"}]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum":[{"rank":0,"name":"node-a"},{"rank":1,"name":"node-b"}]}`, nil).Once()
	// A successful `ceph mon rm` reply is sent only after the monmap proposal
	// commits. The removed daemon may already be exiting and unable to run a
	// follow-up client command, so no post-removal readback is expected here.
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "rm", "node-a").
		Return("", nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMonAbsentAcceptsAmbiguousSuccessWithDetachedVerification(t *testing.T) {
	r := withMockRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"},{"name":"node-b"}]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum_names":["node-a","node-b"]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "rm", "node-a").
		Run(func(_ mock.Arguments) { cancel() }).
		Return("", context.Canceled).Once()
	// The command response was lost and cancelled the request context. The
	// postcondition check must use a fresh bounded context, not the cancelled
	// parent, and proves that membership removal committed.
	liveBoundedContext := mock.MatchedBy(func(checkCtx context.Context) bool {
		_, hasDeadline := checkCtx.Deadline()
		return checkCtx.Err() == nil && hasDeadline
	})
	r.On("RunCommandContext", liveBoundedContext, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-b"}]}`, nil).Once()

	err := ensureMonAbsent(ctx, "node-a")
	assert.NoError(t, err)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestEnsureMonAbsentPreservesRemovalFailure(t *testing.T) {
	r := withMockRunner(t)
	removeErr := errors.New("monmap update failed")
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"},{"name":"node-b"}]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(`{"quorum_names":["node-a","node-b"]}`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "rm", "node-a").
		Return("", removeErr).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":[{"name":"node-a"},{"name":"node-b"}]}`, nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.ErrorIs(t, err, removeErr)
}

func TestEnsureMonAbsentRejectsMalformedMonmap(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
		Return(`{"mons":`, nil).Once()

	err := ensureMonAbsent(context.Background(), "node-a")
	assert.ErrorContains(t, err, "failed to parse monitor map")
}

func TestEnsureMonAbsentRejectsIncompleteMonmap(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantErrMsg string
	}{
		{
			name:       "null document",
			output:     `null`,
			wantErrMsg: `missing "mons" field`,
		},
		{
			name:       "missing monitors",
			output:     `{}`,
			wantErrMsg: `missing "mons" field`,
		},
		{
			name:       "null monitors",
			output:     `{"mons":null}`,
			wantErrMsg: `"mons" must be a non-null array`,
		},
		{
			name:       "non-array monitors",
			output:     `{"mons":{}}`,
			wantErrMsg: `invalid "mons" field`,
		},
		{
			name:       "empty monitors",
			output:     `{"mons":[]}`,
			wantErrMsg: `"mons" must contain at least one monitor`,
		},
		{
			name:       "monitor without name",
			output:     `{"mons":[{}]}`,
			wantErrMsg: "monitor at index 0 has no name",
		},
		{
			name:       "monitor with null name",
			output:     `{"mons":[{"name":null}]}`,
			wantErrMsg: "monitor at index 0 has no name",
		},
		{
			name:       "monitor with empty name",
			output:     `{"mons":[{"name":""}]}`,
			wantErrMsg: "monitor at index 0 has no name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := withMockRunner(t)
			r.On("RunCommandContext", mock.Anything, "ceph", "mon", "dump", "-f", "json").
				Return(test.output, nil).Once()

			err := ensureMonAbsent(context.Background(), "node-a")

			assert.ErrorContains(t, err, test.wantErrMsg)
		})
	}
}
