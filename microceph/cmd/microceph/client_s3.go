package main

import (
	"context"
	"fmt"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
	"github.com/tidwall/gjson"
)

type cmdClientS3 struct {
	common *CmdControl
	client *cmdClient
}

type cmdClientS3Get struct {
	common     *CmdControl
	client     *cmdClient
	s3         *cmdClientS3
	jsonOutput bool
}

type cmdClientS3Create struct {
	common     *CmdControl
	client     *cmdClient
	s3         *cmdClientS3
	accessKey  string
	secret     string
	jsonOutput bool
}

type cmdClientS3Delete struct {
	common *CmdControl
	client *cmdClient
	s3     *cmdClientS3
}

type cmdClientS3List struct {
	common *CmdControl
	client *cmdClient
	s3     *cmdClientS3
}

// parent s3 command handle
func (c *cmdClientS3) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "s3",
		Short: "Manage S3 users for Object storage",
	}

	// Create
	s3CreateCmd := cmdClientS3Create{common: c.common, client: c.client, s3: c}
	cmd.AddCommand(s3CreateCmd.Command())

	// Delete
	s3DeleteCmd := cmdClientS3Delete{common: c.common, client: c.client, s3: c}
	cmd.AddCommand(s3DeleteCmd.Command())

	// Get
	s3GetCmd := cmdClientS3Get{common: c.common, client: c.client, s3: c}
	cmd.AddCommand(s3GetCmd.Command())

	// List
	s3ListCmd := cmdClientS3List{common: c.common, client: c.client, s3: c}
	cmd.AddCommand(s3ListCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

// s3 Get command handle
func (c *cmdClientS3Get) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <NAME>",
		Short: "Fetch details of an existing S3 user",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.jsonOutput, "json", false, "Provide output in json format")
	return cmd
}

func (c *cmdClientS3Get) Run(cmd *cobra.Command, args []string) error {
	// Get should be called with a single name param.
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to fetch S3 user: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	input := &types.S3User{Name: args[0]}
	user, err := client.GetS3User(context.Background(), cli, input)
	if err != nil {
		return err
	}

	err = renderOutput(user, c.jsonOutput)
	if err != nil {
		return err
	}

	return nil
}

// s3 create command handle
func (c *cmdClientS3Create) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <NAME>",
		Short: "Create a new S3 user",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.accessKey, "access-key", "", "custom access-key for new S3 user.")
	cmd.Flags().StringVar(&c.secret, "secret", "", "custom secret for new S3 user.")
	cmd.Flags().BoolVar(&c.jsonOutput, "json", false, "Provide output in json format")
	return cmd
}

func (c *cmdClientS3Create) Run(cmd *cobra.Command, args []string) error {
	// Get should be called with a single name param.
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to create S3 user: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Create a user with given keys.
	input := &types.S3User{
		Name:   args[0],
		Key:    c.accessKey,
		Secret: c.secret,
	}
	user, err := client.CreateS3User(context.Background(), cli, input)
	if err != nil {
		return err
	}

	err = renderOutput(user, c.jsonOutput)
	if err != nil {
		return err
	}

	return nil
}

// s3 delete command handle
func (c *cmdClientS3Delete) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <NAME>",
		Short: "Delete an existing S3 user",
		RunE:  c.Run,
	}
	return cmd
}

func (c *cmdClientS3Delete) Run(cmd *cobra.Command, args []string) error {
	// Get should be called with a single name param.
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to delete S3 user: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteS3User(context.Background(), cli, &types.S3User{Name: args[0]})
	if err != nil {
		return err
	}

	return nil
}

// s3 list command handle
func (c *cmdClientS3List) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all existing S3 users",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClientS3List) Run(cmd *cobra.Command, args []string) error {
	// Should not be called with any params
	if len(args) > 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to list S3 users: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	users, err := client.ListS3Users(context.Background(), cli)
	if err != nil {
		return err
	}

	data := make([][]string, len(users))
	for i := range users {
		data[i] = []string{fmt.Sprintf("%d", i+1), users[i]}
	}

	header := []string{"#", "Name"}
	err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, users)
	if err != nil {
		return err
	}

	return nil
}

func renderOutput(output string, isJson bool) error {
	if isJson {
		fmt.Print(output)
	} else {
		user := types.S3User{
			Name:   gjson.Get(output, "keys.0.user").Str,
			Key:    gjson.Get(output, "keys.0.access_key").Str,
			Secret: gjson.Get(output, "keys.0.secret_key").Str,
		}
		err := renderSingleS3User(user)
		if err != nil {
			return err
		}
	}
	return nil
}

func renderSingleS3User(user types.S3User) error {
	data := make([][]string, 1)
	data[0] = []string{user.Name, user.Key, user.Secret}

	header := []string{"Name", "Access Key", "Secret"}
	err := lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, user)
	if err != nil {
		return err
	}
	return nil
}
