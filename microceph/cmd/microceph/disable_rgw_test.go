package main

import (
	"context"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	tests2 "github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/mock"
)

// make sure it fails like before on empty config
func TestCmdDisableRGWExecute(t *testing.T) {
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
			c := &cmdDisableRGW{
				common: tt.common,
			}
			if err := c.Command().ExecuteContext(context.Background()); (err != nil) && err.Error() != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCmdDisableRGWRun(t *testing.T) {
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
			Enabled: false,
		}
		apiMock.On("EnableRGW", mock.Anything, req).Return(nil)
		c := &cmdDisableRGW{
			apiClient: &apiMock,
		}

		if err := c.Run(c.Command(), []string{}); (err != nil) != tt.wantErr {
			t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
		}
	}
}
