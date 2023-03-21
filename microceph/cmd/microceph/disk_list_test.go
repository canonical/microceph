package main

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/api/types"
	tests2 "github.com/canonical/microceph/microceph/tests"
	"github.com/lxc/lxd/shared/api"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"io"
	"os"
	"strconv"
	"testing"
)

// make sure it fails like before on empty config
func Test_cmdDiskList_Execute(t *testing.T) {
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
			c := &cmdDiskList{
				common: tt.common,
				disk:   nil,
			}
			if err := c.Command().ExecuteContext(context.Background()); (err != nil) && err.Error() != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_cmdDiskList_Run(t *testing.T) {

	type disks struct {
		data  types.Disks
		error error
	}

	type resources struct {
		data  *api.ResourcesStorage
		error error
	}

	tests := []struct {
		name          string
		mockDisks     disks
		mockResources resources
		wantErr       bool
	}{
		{
			name: "Success Empty List",
			mockDisks: disks{
				data:  types.Disks{},
				error: nil,
			},
			mockResources: resources{
				data:  &api.ResourcesStorage{},
				error: nil,
			},
			wantErr: false,
		},
		{
			name: "Success 2 Disks",
			mockDisks: disks{
				data: []types.Disk{
					{
						OSD:      0,
						Path:     "/tmp/folder-1",
						Location: "node-1",
					},
					{
						OSD:      1,
						Path:     "/tmp/folder-2",
						Location: "node-2",
					},
				},
				error: nil,
			},
			mockResources: resources{
				data:  &api.ResourcesStorage{},
				error: nil,
			},
			wantErr: false,
		},
		{
			name: "Success 2 Resources",
			mockDisks: disks{
				data:  types.Disks{},
				error: nil,
			},
			mockResources: resources{
				data: &api.ResourcesStorage{
					Disks: []api.ResourcesStorageDisk{
						{
							Model:  "Virtual Warp Drive",
							Size:   1000,
							ID:     uuid.NewUUID().String(),
							Device: "/dev/sda1",
						},
						{
							Model:  "Virtual Flux Drive",
							Size:   1000,
							ID:     uuid.NewUUID().String(),
							Device: "/dev/sdb1",
						},
					},
					Total: 0,
				},
				error: nil,
			},
			wantErr: false,
		},
		{
			name: "Success 2 Resources / 3 Disks",
			mockDisks: disks{
				data: []types.Disk{
					{
						OSD:      0,
						Path:     "/tmp/folder-1",
						Location: "node-1",
					},
					{
						OSD:      1,
						Path:     "/tmp/folder-2",
						Location: "node-2",
					},
					{
						OSD:      2,
						Path:     "/tmp/folder-3",
						Location: "node-3",
					},
				},
				error: nil,
			},
			mockResources: resources{
				data: &api.ResourcesStorage{
					Disks: []api.ResourcesStorageDisk{
						{
							Model:  "Virtual Warp Drive",
							Size:   1000,
							ID:     uuid.NewUUID().String(),
							Device: "/dev/sda1",
							Type:   "warp",
						},
						{
							Model:  "Virtual Flux Drive",
							Size:   1000,
							ID:     uuid.NewUUID().String(),
							Device: "/dev/sdb1",
							Type:   "flux",
						},
					},
					Total: 0,
				},
				error: nil,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiMock := tests2.ApiMock{}
			apiMock.On("GetDisks", mock.Anything).Return(tt.mockDisks.data, tt.mockDisks.error)
			apiMock.On("GetResources", mock.Anything).Return(tt.mockResources.data, tt.mockResources.error)
			c := &cmdDiskList{
				apiClient: apiMock,
			}

			saveStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			if err := c.Run(c.Command(), []string{}); (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}

			w.Close()
			out, _ := io.ReadAll(r)
			os.Stdout = saveStdout

			outputString := string(out)

			for _, disk := range tt.mockDisks.data {
				assert.Contains(t, outputString, strconv.FormatInt(disk.OSD, 10))
				assert.Contains(t, outputString, disk.Location)
				assert.Contains(t, outputString, disk.Path)
			}

			for _, disk := range tt.mockResources.data.Disks {
				assert.Contains(t, outputString, disk.Model)
				assert.Contains(t, outputString, fmt.Sprintf("%dB", disk.Size))
				assert.Contains(t, outputString, disk.Type)
				assert.Contains(t, outputString, disk.DevicePath)
			}

			println(outputString)

		})
	}
}
