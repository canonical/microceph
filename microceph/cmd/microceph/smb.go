package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

// cmdSMB is a hidden debug command family: the supported control plane is
// mgr/smb (ceph smb ...) via the orchestrator. These commands drive the
// microcephd endpoints directly for development and support.
type cmdSMB struct {
	common *CmdControl
}

func (c *cmdSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "smb",
		Short:  "Debug commands for the SMB deployment backend",
		Hidden: true,
	}

	smbApplyCmd := cmdSMBApply{common: c.common}
	smbRmCmd := cmdSMBRm{common: c.common}
	smbListCmd := cmdSMBList{common: c.common}
	cmd.AddCommand(smbApplyCmd.Command())
	cmd.AddCommand(smbRmCmd.Command())
	cmd.AddCommand(smbListCmd.Command())
	return cmd
}

// localClient returns a client to the local microcephd socket.
func smbLocalClient(common *CmdControl) (*microcluster.MicroCluster, error) {
	return microcluster.App(microcluster.Args{StateDir: common.FlagStateDir})
}

type cmdSMBApply struct {
	common *CmdControl
}

func (c *cmdSMBApply) Command() *cobra.Command {
	return &cobra.Command{
		Use:   "apply-spec <spec.json>",
		Short: "Apply an SMBSpec JSON file to the cluster",
		Args:  cobra.ExactArgs(1),
		RunE:  c.Run,
	}
}

// Run handles the smb apply-spec command.
func (c *cmdSMBApply) Run(cmd *cobra.Command, args []string) error {
	spec, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}

	if !json.Valid(spec) {
		return fmt.Errorf("%s is not valid JSON", args[0])
	}

	m, err := smbLocalClient(c.common)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	return client.ApplySMBSpec(context.Background(), cli, spec)
}

type cmdSMBRm struct {
	common *CmdControl
}

func (c *cmdSMBRm) Command() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <cluster-id>",
		Short: "Remove an SMB cluster from all its member nodes",
		Args:  cobra.ExactArgs(1),
		RunE:  c.Run,
	}
}

// Run handles the smb rm command.
func (c *cmdSMBRm) Run(cmd *cobra.Command, args []string) error {
	if !types.SMBClusterIDRegex.MatchString(args[0]) {
		return fmt.Errorf("'%s' is not a valid cluster id (regex: '%s')", args[0], types.SMBClusterIDRegex.String())
	}

	m, err := smbLocalClient(c.common)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	return client.RemoveSMBService(context.Background(), cli, &types.SMBService{ClusterID: args[0]})
}

type cmdSMBList struct {
	common *CmdControl
}

func (c *cmdSMBList) Command() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List SMB clusters with their specs and placement",
		RunE:  c.Run,
	}
}

// Run handles the smb list command.
func (c *cmdSMBList) Run(cmd *cobra.Command, args []string) error {
	m, err := smbLocalClient(c.common)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	statuses, err := client.GetSMBServices(context.Background(), cli)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(out))
	return nil
}
