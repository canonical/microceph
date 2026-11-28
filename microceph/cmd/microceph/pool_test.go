package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPoolSetRFArguments checks the Args validator of "pool set-rf", where the pools may be
// omitted when --set-default is given.
func TestPoolSetRFArguments(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string // The flags and arguments typed after "pool set-rf".
		wantErr string   // Empty when the command line must be accepted.
	}{
		{name: "no pools", cmdline: []string{"--size", "2"}, wantErr: "no pools given"},
		{name: "one pool", cmdline: []string{"--size", "2", "mypool"}},
		{name: "all pools", cmdline: []string{"--size", "2", "*"}},
		{name: "empty string", cmdline: []string{"--size", "2", ""}},
		{name: "only set default", cmdline: []string{"--size", "2", "--set-default"}},
		{name: "pools and set default", cmdline: []string{"--size", "2", "--set-default", "mypool"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCommand()
			cmd, _, err := root.Find([]string{"pool", "set-rf"})
			require.NoError(t, err)
			require.NoError(t, cmd.ParseFlags(tc.cmdline))

			err = cmd.ValidateArgs(cmd.Flags().Args())

			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestSetsDefault checks which "pool set-rf" invocations also set the default pool size.
func TestSetsDefault(t *testing.T) {
	tests := []struct {
		name           string
		flagSetDefault bool
		pools          []string
		want           bool
	}{
		{name: "named pools", pools: []string{"mypool", "other"}, want: false},
		{name: "all pools", pools: []string{"*"}, want: false},
		{name: "empty string", pools: []string{""}, want: true},
		{name: "blank string", pools: []string{" "}, want: true},
		{name: "empty string among pools", pools: []string{"mypool", ""}, want: true},
		{name: "flag without pools", flagSetDefault: true, want: true},
		{name: "flag with pools", flagSetDefault: true, pools: []string{"mypool"}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, setsDefault(tc.flagSetDefault, tc.pools))
		})
	}
}
