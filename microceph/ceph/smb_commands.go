package ceph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/common"
)

const smbCommandTimeout = time.Minute
const smbCleanupTimeout = 2 * time.Minute

// runSMBCommand bounds node-local commands even when the caller has no deadline.
func runSMBCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smbCommandTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	output, err := common.ProcessExec.RunCommandContext(ctx, name, args...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return output, err
}

// HospitalityCheck has no context parameter; still bound its local probes.
func smbInterfaceConnected(name string) bool {
	_, err := runSMBCommand(context.Background(), "snapctl", "is-connected", name)
	return err == nil
}

func smbSnapAction(ctx context.Context, action, service string, enabled bool) error {
	args := []string{action, "microceph." + service}
	if enabled {
		flag := "--enable"
		if action == "stop" {
			flag = "--disable"
		}
		args = append(args, flag)
	}
	_, err := runSMBCommand(ctx, "snapctl", args...)
	return err
}

func smbSnapCheckActive(ctx context.Context, service string) error {
	out, err := runSMBCommand(ctx, "snapctl", "services", "microceph."+service)
	if err != nil {
		return err
	}
	if strings.Contains(out, "inactive") || !strings.Contains(out, "active") {
		return fmt.Errorf("%s service is not active", service)
	}
	return nil
}
