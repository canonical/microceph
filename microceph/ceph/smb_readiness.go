package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/common"
)

const smbReadinessTimeout = 5 * time.Minute
const smbReadinessProbeTimeout = 5 * time.Second

var smbLocalCTDBReadyRegex = regexp.MustCompile(`(?m)^pnn:[0-9]+\s+\S+\s+OK \(THIS NODE\)\s*$`)

func smbCTDBLocallyReady(status string) bool {
	return smbLocalCTDBReadyRegex.MatchString(status) && strings.Contains(status, "Recovery mode:NORMAL (0)")
}

func waitForSMBServiceReady(ctx context.Context, service string, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, smbReadinessTimeout)
	defer cancel()
	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("SMB service %s not ready (%v): %w", service, lastErr, ctx.Err())
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, smbReadinessProbeTimeout)
		lastErr = probeSMBServiceReady(probeCtx, service)
		probeCancel()
		if lastErr == nil {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func checkSMBCTDBPNN(ctx context.Context, expected int) error {
	ctx, cancel := context.WithTimeout(ctx, smbReadinessProbeTimeout)
	defer cancel()
	command := filepath.Join(os.Getenv("SNAP"), "commands", "samba-command")
	output, err := runSMBCommand(ctx, command, "ctdb", "pnn")
	if err != nil {
		return fmt.Errorf("failed to verify CTDB PNN: %w", err)
	}
	actual, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || actual != expected {
		return fmt.Errorf("CTDB PNN does not match assigned rank %d", expected)
	}
	return nil
}

func probeSMBServiceReady(ctx context.Context, service string) error {
	output, err := common.ProcessExec.RunCommandContext(ctx, "snapctl", "services", "microceph."+service)
	if err != nil {
		return fmt.Errorf("failed checking %s activity", service)
	}
	active := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "microceph."+service && fields[2] == "active" {
			active = true
		}
	}
	if !active {
		return fmt.Errorf("%s is not active", service)
	}
	command := filepath.Join(os.Getenv("SNAP"), "commands", "samba-command")
	switch service {
	case "smbd":
		output, err = common.ProcessExec.RunCommandContext(ctx, command, "smbcontrol", "smbd", "ping")
		if err != nil || !strings.Contains(output, "PONG") {
			return fmt.Errorf("smbd has not responded to its local control probe")
		}
	case "ctdbd":
		output, err = common.ProcessExec.RunCommandContext(ctx, command, "ctdb", "status")
		if err != nil || !smbCTDBLocallyReady(output) {
			return fmt.Errorf("local CTDB node is not healthy or is recovering")
		}
	}
	return nil
}
