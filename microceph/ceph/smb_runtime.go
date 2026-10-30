package ceph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/common"
)

// smbService controls a snap-managed service; its lifetime is not owned by Go.
type smbService interface {
	// Start explicitly starts and enables the snap service.
	Start(context.Context) error
	// Ready waits for functional readiness without starting or restarting it.
	Ready(context.Context) error
	// Stop explicitly stops/disables the service and confirms it is inactive.
	Stop(context.Context) error
}

type smbSnapService struct {
	name string
	root string
}

// newSMBServiceTable constructs fresh role implementations, without service I/O.
// Dependency ordering belongs to the lifecycle plan, not map iteration.
func newSMBServiceTable(root string) map[string]smbService {
	return map[string]smbService{
		"smbd":  smbSnapService{name: "smbd", root: root},
		"ctdbd": smbSnapService{name: "ctdbd", root: root},
	}
}

// Start starts and enables this independently supervised snap service.
func (s smbSnapService) Start(ctx context.Context) error {
	_, err := runSMBServiceCommand(ctx, "start", "microceph."+s.name, "--enable")
	return err
}

// Stop stops/disables this service and separately confirms its inactive state.
func (s smbSnapService) Stop(ctx context.Context) error {
	_, err := runSMBServiceCommand(ctx, "stop", "microceph."+s.name, "--disable")
	if err != nil {
		return err
	}
	return confirmSMBInactive(ctx, s.name)
}

func confirmSMBInactive(ctx context.Context, role string) error {
	out, err := runSMBServiceCommand(ctx, "services", "microceph."+role)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "microceph."+role && fields[2] == "inactive" {
			return nil
		}
	}
	return fmt.Errorf("SMB %s exit is unconfirmed", role)
}

// Ready observes functional readiness without changing service intent.
func (s smbSnapService) Ready(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		err := waitCtx.Err()
		if err == nil {
			err = smbReadyNativeFunc(waitCtx, s.root, s.name)
		}
		if err == nil {
			// A successful probe racing cancellation is not fresh health.
			return waitCtx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s readiness timed out", s.name)
		case <-tick.C:
		}
	}
}

// runSMBServiceCommand bounds only the transient snapctl client. It never owns
// the lifetime of the upstream service that snapd starts independently.
func runSMBServiceCommand(ctx context.Context, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if commandCtx.Err() != nil {
		return "", commandCtx.Err()
	}
	out, err := common.ProcessExec.RunCommandContext(commandCtx, "snapctl", args...)
	if commandCtx.Err() != nil {
		return "", commandCtx.Err()
	}
	return out, err
}
