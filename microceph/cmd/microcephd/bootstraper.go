package main

import (
	"context"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
)

// Bootstraper encapsulates the bootstrap
type Bootstraper interface {
	Prefill(bd common.BootstrapConfig) error
	Precheck(ctx context.Context, state interfaces.StateInterface) error
	Bootstrap(ctx context.Context, state interfaces.StateInterface) error
}

// GetBootstraper returns a bootstraper based on the bootstrap parameters.
func GetBootstraper(bd common.BootstrapConfig) Bootstraper {
	sb := SimpleBootstraper{}
	sb.Prefill(bd)

	return &sb
}
