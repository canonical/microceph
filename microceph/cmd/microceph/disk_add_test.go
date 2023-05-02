package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/canonical/microceph/microceph/api/types"
	tests2 "github.com/canonical/microceph/microceph/mocks"
)

// make sure it fails like before on empty config
func TestCmdDiskAddExecute(t *testing.T) {
	tests := []struct {
		name    string
		common  *CmdControl
		wantErr string
	}{
		{
			"failure constructing app without state dir",
			&CmdControl{
				FlagStateDir: "",
			},
			"Missing state directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &cmdDiskAdd{
				common: tt.common,
			}
			if err := c.Command().ExecuteContext(context.Background()); (err != nil) && err.Error() != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCmdDiskAddRun(t *testing.T) {
	tests := []struct {
		name    string
		wipe    bool
		wantErr bool
	}{
		{
			name:    "Success Add with wipe",
			wipe:    true,
			wantErr: false,
		},
		{
			name:    "Success Add without wipe",
			wipe:    false,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		apiMock := tests2.ApiMock{}
		path := "/tmp/disk"
		req := &types.DisksPost{
			Path: path,
			Wipe: tt.wipe,
		}
		apiMock.On("AddDisk", mock.Anything, req).Return(nil)

		c := &cmdDiskAdd{
			apiClient: &apiMock,
		}
		cmd := c.Command()
		c.flagWipe = tt.wipe

		if err := c.Run(cmd, []string{path}); (err != nil) != tt.wantErr {
			t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
		}
	}
}
