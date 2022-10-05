package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/microcluster/microcluster"
	"github.com/lxc/lxd/lxd/util"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/spf13/cobra"
)

type cmdInit struct {
	common *CmdControl

	flagBootstrap bool
	flagToken     string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive configuration of MicroCeph",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Connect to the daemon.
	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Check if already initialized.
	_, err = client.GetClusterMembers(context.Background())
	isUninitialized := err.Error() == "Daemon not yet initialized"
	if err != nil && !isUninitialized {
		return err
	}

	// User interaction.
	mode := "existing"

	if isUninitialized {
		// Get system name.
		hostName, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("Failed to retrieve system hostname: %w", err)
		}

		// Get system address.
		address := util.NetworkInterfaceAddress()
		address, err = cli.AskString(fmt.Sprintf("Please choose the address MicroCeph will be listening on [default=%s]: ", address), address, nil)
		if err != nil {
			return err
		}
		address = util.CanonicalNetworkAddress(address, 7000)

		wantsBootstrap, err := cli.AskBool("Would you like to create a new MicroCeph cluster? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if wantsBootstrap {
			mode = "bootstrap"

			// Offer overriding the name.
			hostName, err = cli.AskString(fmt.Sprintf("Please choose a name for this system [default=%s]: ", hostName), hostName, nil)
			if err != nil {
				return err
			}

			// Bootstrap the cluster.
			err = m.NewCluster(hostName, address, time.Second*30)
			if err != nil {
				return err
			}
		} else {
			mode = "join"

			token, err := cli.AskString("Please enter your join token: ", "", nil)
			if err != nil {
				return err
			}

			err = m.JoinCluster(hostName, address, token, time.Second*30)
			if err != nil {
				return err
			}
		}
	} else {
		fmt.Printf("MicroCeph has already been initialized.\n\n")
	}

	if mode != "join" {
		wantsMachines, err := cli.AskBool("Would you like to add additional servers to the cluster? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if wantsMachines {
			for {
				tokenName, err := cli.AskString("What's the name of the new MicroCeph server? (empty to exit): ", "", func(input string) error { return nil })
				if err != nil {
					return err
				}

				if tokenName == "" {
					break
				}

				// Issue the token.
				token, err := m.NewJoinToken(tokenName)
				if err != nil {
					return err
				}

				fmt.Println(token)
			}
		}
	}

	return nil
}
