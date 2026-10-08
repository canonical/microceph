package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argCountCases lists the commands whose argument count cobra validates before the command runs.
// A maxArgs of -1 means the command takes any number of arguments from minArgs upwards.
// "disk add" is not listed because its arguments depend on its flags, see TestDiskAddArguments.
var argCountCases = []struct {
	path    string
	minArgs int
	maxArgs int
}{
	{"client config get", 1, 1},
	{"client config list", 0, 0},
	{"client config reset", 1, 1},
	{"client config set", 2, 2},
	{"cluster add", 1, 1},
	{"cluster bootstrap", 0, 0},
	{"cluster config get", 1, 1},
	{"cluster config list", 0, 0},
	{"cluster config reset", 1, 1},
	{"cluster config set", 2, 2},
	{"cluster export", 1, 1},
	{"cluster join", 1, 1},
	{"cluster maintenance enter", 1, 1},
	{"cluster maintenance exit", 1, 1},
	{"cluster migrate", 2, 2},
	{"cluster remove", 1, 1},
	{"cluster sql", 1, 1},
	{"disk encryption-support", 0, 0},
	{"disk remove", 1, 1},
	{"log get-level", 0, 0},
	{"log set-level", 1, 1},
	{"pool list", 0, 0},
	{"pool set-rf", 1, -1},
	{"remote import", 2, 2},
	{"remote list", 0, 0},
	{"remote remove", 1, 1},
	{"replication configure rbd", 1, 1},
	{"replication demote", 0, 0},
	{"replication disable cephfs", 0, 0},
	{"replication disable rbd", 1, 1},
	{"replication enable cephfs", 0, 0},
	{"replication enable rbd", 1, 1},
	{"replication list cephfs", 0, 0},
	{"replication list rbd", 0, 0},
	{"replication promote", 0, 0},
	{"replication status cephfs", 1, 1},
	{"replication status rbd", 1, 1},
}

// executeRoot runs the real command tree with args and returns everything it printed with its error.
func executeRoot(args ...string) (string, error) {
	root := newRootCommand()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	err := root.Execute()
	return out.String(), err
}

// TestCommandsRejectAWrongArgumentCount checks that a wrong argument count fails the Args validator
// of each command in the real tree, and a correct one passes it.
func TestCommandsRejectAWrongArgumentCount(t *testing.T) {
	root := newRootCommand()

	for _, tc := range argCountCases {
		t.Run(tc.path, func(t *testing.T) {
			cmd, _, err := root.Find(strings.Fields(tc.path))
			require.NoError(t, err)
			require.Equal(t, "microceph "+tc.path, cmd.CommandPath(), "the path does not name a command")

			for count := 0; count <= 4; count++ {
				err := cmd.ValidateArgs(make([]string, count))

				accepted := count >= tc.minArgs && (tc.maxArgs < 0 || count <= tc.maxArgs)
				if accepted {
					assert.NoError(t, err, "%d arguments must be accepted", count)
				} else {
					assert.Error(t, err, "%d arguments must be rejected", count)
				}
			}
		})
	}
}

// TestDiskAddArguments checks the Args validator of "disk add", where the devices can also come
// from --all-available or --osd-match.
func TestDiskAddArguments(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string // The flags and arguments typed after "disk add".
		wantErr string   // Empty when the command line must be accepted.
	}{
		{name: "no arguments", wantErr: "no disks given"},
		{name: "only a flag", cmdline: []string{"--wipe"}, wantErr: "no disks given"},
		{name: "one device", cmdline: []string{"/dev/sdb"}},
		{name: "several devices", cmdline: []string{"/dev/sdb", "/dev/sdc", "--wipe"}},
		{name: "loop spec", cmdline: []string{"loop,4G,3"}},
		{name: "all available", cmdline: []string{"--all-available"}},
		{name: "osd match", cmdline: []string{"--osd-match", "eq(@type, 'nvme')"}},
		{
			name:    "osd match and a device",
			cmdline: []string{"--osd-match", "eq(@type, 'nvme')", "/dev/sdb"},
			wantErr: "cannot be used with positional device arguments",
		},
		{
			name:    "flag conflict wins over the missing device",
			cmdline: []string{"--wal-match", "eq(@size, 20GiB)"},
			wantErr: "--wal-match requires --osd-match",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCommand()
			cmd, _, err := root.Find([]string{"disk", "add"})
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

// TestDiskAddReportsAFlagConflictWithoutUsage checks that an invalid flag combination is a one-line
// error, as it was when Run reported it, while a missing device prints the usage like any other
// argument count error. The usage lists every flag, so it would match the greps on these messages in
// tests/scripts/test_dsl_functest.sh whatever the error says.
func TestDiskAddReportsAFlagConflictWithoutUsage(t *testing.T) {
	tests := []struct {
		name      string
		cmdline   []string
		wantErr   string
		wantUsage bool
	}{
		{"no device", []string{"disk", "add"}, "no disks given", true},
		{
			"osd match and a device",
			[]string{"disk", "add", "--osd-match", "eq(@type, 'nvme')", "/dev/sdb"},
			"--osd-match/--wal-match/--db-match cannot be used with positional device arguments", false,
		},
		{
			"wal match without osd match",
			[]string{"disk", "add", "--wal-match", "eq(@size, 20GiB)"},
			"--wal-match requires --osd-match", false,
		},
		{
			"wal match without wal size",
			[]string{"disk", "add", "--osd-match", "eq(@size, 10GiB)", "--wal-match", "eq(@size, 20GiB)"},
			"--wal-match requires --wal-size", false,
		},
		{
			"wal wipe without wal match",
			[]string{"disk", "add", "--osd-match", "eq(@size, 10GiB)", "--wal-wipe"},
			"--wal-wipe requires --wal-match or --wal-device", false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := executeRoot(tc.cmdline...)

			require.ErrorContains(t, err, tc.wantErr)
			assert.Contains(t, out, "Error: "+tc.wantErr)
			if tc.wantUsage {
				assert.Contains(t, out, "Usage:")
			} else {
				assert.NotContains(t, out, "Usage:")
			}
		})
	}
}

// commandGroups returns the path below the root of every command that only groups subcommands.
func commandGroups(cmd *cobra.Command) [][]string {
	var groups [][]string

	for _, sub := range cmd.Commands() {
		if sub.HasSubCommands() {
			groups = append(groups, strings.Fields(sub.CommandPath())[1:])
		}
		groups = append(groups, commandGroups(sub)...)
	}

	return groups
}

// TestCommandGroupsRejectUnknownSubcommands checks that a command group prints its usage when it is
// called alone, and fails when it is called with a subcommand that does not exist.
func TestCommandGroupsRejectUnknownSubcommands(t *testing.T) {
	groups := commandGroups(newRootCommand())

	var names []string
	for _, group := range groups {
		names = append(names, strings.Join(group, " "))
	}
	require.Subset(t, names, []string{"cluster", "disk", "replication", "replication enable", "replication configure"})

	for _, group := range groups {
		t.Run(strings.Join(group, " "), func(t *testing.T) {
			out, err := executeRoot(group...)
			require.NoError(t, err)
			assert.Contains(t, out, "Usage:")

			typo := append(append([]string{}, group...), "bogus")
			_, err = executeRoot(typo...)
			require.Error(t, err)
			assert.ErrorContains(t, err, `unknown command "bogus"`)
		})
	}
}

// TestRemoteImportWithoutTokenIsAUsageError is the case of the cluster export flake: an empty token
// left remote import with one argument, and it printed its help and exited 0.
func TestRemoteImportWithoutTokenIsAUsageError(t *testing.T) {
	out, err := executeRoot("remote", "import", "siteb", "--local-name=sitea")

	require.Error(t, err)
	assert.ErrorContains(t, err, "accepts 2 arg(s), received 1")
	assert.Contains(t, out, "Error: accepts 2 arg(s), received 1")
	assert.Contains(t, out, "microceph remote import <name> <token>", "the usage line is missing")
}

// TestUnknownFlagIsAUsageError checks that flag parsing errors print usage before RunE.
func TestUnknownFlagIsAUsageError(t *testing.T) {
	out, err := executeRoot("remote", "import", "--bogus")

	require.ErrorContains(t, err, "unknown flag: --bogus")
	assert.Contains(t, out, "Error: unknown flag: --bogus")
	assert.Contains(t, out, "Usage:")
}

// TestUsageIsNotPrintedWhenARunningCommandFails checks that only usage errors print the usage.
func TestUsageIsNotPrintedWhenARunningCommandFails(t *testing.T) {
	// Run fails on the missing --local-name before it contacts the daemon.
	out, err := executeRoot("remote", "import", "siteb", "sometoken")

	require.Error(t, err)
	assert.ErrorContains(t, err, "--local-name")
	assert.NotContains(t, out, "Usage:")
}

// TestHelpFlagPrintsUsageWithoutAnArgumentCount checks that --help is not an argument count error.
func TestHelpFlagPrintsUsageWithoutAnArgumentCount(t *testing.T) {
	out, err := executeRoot("remote", "import", "--help")

	require.NoError(t, err)
	assert.Contains(t, out, "microceph remote import <name> <token>")
}

// TestMainExitStatus runs main in a child process, because main ends the process with the exit status.
func TestMainExitStatus(t *testing.T) {
	if os.Getenv("MICROCEPH_TEST_MAIN_ARGS") != "" {
		os.Args = append([]string{"microceph"}, strings.Fields(os.Getenv("MICROCEPH_TEST_MAIN_ARGS"))...)
		main()
		return
	}

	tests := []struct {
		name     string
		cmdline  string
		wantExit int
		wantOut  string
	}{
		{"remote import without a token", "remote import siteb --local-name=sitea", 1, "accepts 2 arg(s), received 1"},
		{"unknown flag", "remote import --bogus", 1, "unknown flag: --bogus"},
		{"remote list with an argument", "remote list siteb", 1, `unknown command "siteb" for "microceph remote list"`},
		{"disk add without a disk", "disk add", 1, "no disks given"},
		{"command group with an unknown subcommand", "replication enable rbdd", 1, `unknown command "rbdd" for "microceph replication enable"`},
		{"command group without a subcommand", "cluster", 0, "Usage:"},
		{"help flag", "remote import --help", 0, "Usage:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestMainExitStatus$")
			child.Env = append(os.Environ(), "MICROCEPH_TEST_MAIN_ARGS="+tc.cmdline)

			out, err := child.CombinedOutput()

			exitCode := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantExit, exitCode, string(out))
			assert.Contains(t, string(out), tc.wantOut)
		})
	}
}
