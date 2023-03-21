package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/canonical/microceph/microceph/api/types"
	tests2 "github.com/canonical/microceph/microceph/mocks"
)

// make sure it fails like before on empty config
func TestCmdEnableRGWExecute(t *testing.T) {
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
			c := &cmdEnableRGW{
				common: tt.common,
			}
			if err := c.Command().ExecuteContext(context.Background()); (err != nil) && err.Error() != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCmdEnableRGWRun(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "ok",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		apiMock := tests2.ApiMock{}
		req := &types.RGWService{
			Port:    80,
			Enabled: true,
		}
		apiMock.On("EnableRGW", mock.Anything, req).Return(nil)
		c := &cmdEnableRGW{
			apiClient: &apiMock,
		}

		if err := c.Run(c.Command(), []string{}); (err != nil) != tt.wantErr {
			t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
		}
	}
}
