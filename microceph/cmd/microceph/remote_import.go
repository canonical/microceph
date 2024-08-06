package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteImport struct {
	common    *CmdControl
	remote    *cmdRemote
	localName string
}

type dict map[string]interface{}

func (c *cmdRemoteImport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <Name> <Token>",
		Short: "Import external MicroCeph cluster as a remote",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().StringVar(&c.localName, "local-name", "", "friendly local name for cluster.")
	return cmd
}

func (c *cmdRemoteImport) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return cmd.Help()
	}

	if len(c.localName) == 0 {
		return fmt.Errorf("please provide a local name using `--local-name` flag")
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Read remote cluster token
	data := dict{}
	jsonContent, err := base64.StdEncoding.DecodeString(args[1])
	if err != nil {
		return err
	}

	err = json.Unmarshal(jsonContent, &data)
	if err != nil {
		return err
	}

	// Prepare payload for API request.
	payload := types.Remote{}
	payload.Init(c.localName, args[0], false) // initialise with local and remote name.
	for key, value := range data {
		payload.Config[key] = fmt.Sprintf("%s", value)
	}

	// send remote import request
	return client.SendRemoteImportRequest(context.Background(), cli, payload)
}
