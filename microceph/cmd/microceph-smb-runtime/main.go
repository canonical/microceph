// The microceph-smb-runtime executable is private to the fixed snap app wrappers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/canonical/microceph/microceph/ceph"
)

func run(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("SMB runtime requires a fixed app role and owner directory")
	}
	return ceph.RunSMBRuntime(ctx, args[0], args[1], args[2:]...)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := run(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
