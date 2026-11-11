package ceph

import (
	"context"
	"testing"
)

func TestRunnerCommandCompatibility(t *testing.T) {
	runner := RunnerImpl{}
	output, err := runner.RunCommand("sh", "-c", "printf '%s' quincy")
	if err != nil || output != "quincy" {
		t.Fatalf("command result changed: output=%q, err=%v", output, err)
	}

	output, err = runner.RunCommandContext(context.Background(), "sh", "-c", "printf '%s' microcluster-v2")
	if err != nil || output != "microcluster-v2" {
		t.Fatalf("context command result changed: output=%q, err=%v", output, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.RunCommandContext(ctx, "sh", "-c", "exit 0")
	if err == nil {
		t.Fatal("canceled command must not execute successfully")
	}
}
