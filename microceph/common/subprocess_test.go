package common

import (
	"context"
	"errors"
	"testing"
)

func TestRunnerImpl(t *testing.T) {
	runner := RunnerImpl{}
	tests := []struct {
		name string
		run  func(string, ...string) (string, error)
	}{
		{name: "RunCommand", run: runner.RunCommand},
		{name: "RunCommandContext", run: func(name string, args ...string) (string, error) {
			return runner.RunCommandContext(context.Background(), name, args...)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("output", func(t *testing.T) {
				output, err := tt.run("sh", "-c", "printf '%s' \"$1\"", "sh", "expected output")
				if err != nil || output != "expected output" {
					t.Fatalf("output = %q, error = %v", output, err)
				}
			})

			t.Run("nonzero exit", func(t *testing.T) {
				output, err := tt.run("sh", "-c", "printf output; exit 7")
				if err == nil || output != "output" {
					t.Fatalf("output = %q, error = %v", output, err)
				}
			})
		})
	}
}

func TestRunnerImplRunCommandContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	output, err := (RunnerImpl{}).RunCommandContext(ctx, "sh", "-c", "printf unexpected")
	if !errors.Is(err, context.Canceled) || output != "" {
		t.Fatalf("output = %q, error = %v; want empty output and context cancellation", output, err)
	}
}
