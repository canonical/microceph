package metrics

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeListenerIPv6(t *testing.T) {
	address, err := nativeListenAddress([]net.IP{net.ParseIP("::1")}, 9922)
	require.NoError(t, err)
	require.Equal(t, "[::1]:9922", address)
	address, err = nativeListenAddress(nil, 9922)
	require.NoError(t, err)
	require.Equal(t, ":9922", address)
	_, err = nativeListenAddress([]net.IP{net.ParseIP("::1"), net.ParseIP("127.0.0.1")}, 9922)
	require.Error(t, err)
}

func TestNativeExplicitSMBStatusAndConfig(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "status")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700))
	t.Setenv("SMBMETRICS_SMBSTATUS", binary)
	t.Setenv("SMBMETRICS_SMBCONF", "/native/config/smb.conf")
	out, err := executeSMBStatusCommand("--processes", "--json")
	require.NoError(t, err)
	require.Equal(t, "--configfile\n/native/config/smb.conf\n--processes\n--json", out)
	t.Setenv("SMBMETRICS_SMBSTATUS", binary+"-absent")
	_, err = LocateSMBStatus()
	require.Error(t, err, "an explicit missing bundled path must never use host smbstatus")
}
