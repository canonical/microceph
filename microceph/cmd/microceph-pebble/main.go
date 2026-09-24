// microceph-pebble is an internal Snap launcher, not a public Snap command.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/microceph/microceph/internal/pebble"
)

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: microceph-pebble <run|status|wait-ready|reload|osd-ready|osd-run|osd-stop> <app|id>")
	}
	runtime, err := pebble.FromEnvironment()
	if err != nil {
		return err
	}
	// Bound controller failures, with headroom beyond the OSD's five-minute
	// child grace. The outer Snap stop timeout is longer still.
	ctx, cancel := context.WithTimeout(context.Background(), 330*time.Second)
	defer cancel()
	switch args[0] {
	case "run":
		return runtime.Run(ctx, args[1])
	case "status":
		return runtime.CheckActive(ctx, args[1])
	case "wait-ready":
		return runtime.WaitReady(ctx, args[1])
	case "reload":
		if args[1] != "osd" {
			return fmt.Errorf("only the OSD app supports reload")
		}
		return runtime.ReloadOSDs(ctx)
	case "osd-ready":
		return runtime.PublishOSD(ctx, args[1])
	case "osd-run":
		return runtime.RunOSD(ctx, args[1])
	case "osd-stop":
		return runtime.StopOSD(ctx, args[1])
	default:
		return fmt.Errorf("unknown internal Pebble operation %q", args[0])
	}
}

func main() {
	err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
