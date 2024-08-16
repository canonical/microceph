package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdInit struct {
	common *CmdControl
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
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	lc, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Check if already initialized.
	_, err = lc.GetClusterMembers(context.Background())
	isUninitialized := err != nil && api.StatusErrorCheck(err, http.StatusServiceUnavailable)
	if err != nil && !isUninitialized {
		return err
	}

	// User interaction.
	mode := "existing"

	if isUninitialized {
		// Get system name.
		hostName, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("failed to retrieve system hostname: %w", err)
		}

		// Get system address.
		address := util.NetworkInterfaceAddress()
		address, err = c.common.Asker.AskString(fmt.Sprintf("Please choose the address MicroCeph will be listening on [default=%s]: ", address), address, nil)
		if err != nil {
			return err
		}
		address = util.CanonicalNetworkAddress(address, 7443)

		wantsBootstrap, err := c.common.Asker.AskBool("Would you like to create a new MicroCeph cluster? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if wantsBootstrap {
			mode = "bootstrap"
			// Offer overriding the name.
			hostName, err = c.common.Asker.AskString(fmt.Sprintf("Please choose a name for this system [default=%s]: ", hostName), hostName, nil)
			if err != nil {
				return err
			}

			// Bootstrap the cluster.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute*2)
			defer cancel()

			err = m.NewCluster(ctx, hostName, address, nil)
			if err != nil {
				return err
			}
		} else {
			mode = "join"

			token, err := c.common.Asker.AskString("Please enter your join token: ", "", nil)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute*2)
			defer cancel()

			err = m.JoinCluster(ctx, hostName, address, token, nil)
			if err != nil {
				return err
			}
		}
	} else {
		fmt.Printf("MicroCeph has already been initialized.\n\n")
	}

	// Add additional servers.
	if mode != "join" {
		wantsMachines, err := c.common.Asker.AskBool("Would you like to add additional servers to the cluster? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if wantsMachines {
			for {
				tokenName, err := c.common.Asker.AskString("What's the name of the new MicroCeph server? (empty to exit): ", "", func(input string) error { return nil })
				if err != nil {
					return err
				}

				if tokenName == "" {
					break
				}

				// Issue the token.
				token, err := m.NewJoinToken(context.Background(), tokenName)
				if err != nil {
					return err
				}

				fmt.Println(token)
			}
		}
	}

	// Add some disks.
	wantsDisks, err := c.common.Asker.AskBool("Would you like to add additional local disks to MicroCeph? (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if wantsDisks {
		err = printLocalDisks(lc)
		if err != nil {
			return err
		}

		for {
			diskPath, err := c.common.Asker.AskString("What's the disk path? (empty to exit): ", "", func(input string) error { return nil })
			if err != nil {
				return err
			}

			if diskPath == "" {
				break
			}

			diskWipe, err := c.common.Asker.AskBool("Would you like the disk to be wiped? [default=no]: ", "no")
			if err != nil {
				return err
			}

			diskEncrypt, err := c.common.Asker.AskBool("Would you like the disk to be encrypted? [default=no]: ", "no")
			if err != nil {
				return err
			}

			// Add the disk.
			req := &types.DisksPost{
				Path:    []string{diskPath},
				Wipe:    diskWipe,
				Encrypt: diskEncrypt,
			}

			failures, err := client.AddDisk(context.Background(), lc, req)
			if err != nil {
				return fmt.Errorf("failed to add the following disks:\n%v \nerr: %w", failures, err)
			}
		}
	}

	return nil
}

func printLocalDisks(cli *microCli.Client) error {
	// List unpartitioned disks.
	availableDisks, err := getUnpartitionedDisks(cli)
	if err != nil {
		return fmt.Errorf("internal error: unable to fetch unpartitioned disks: %w", err)
	}

	return outputFormattedTable(nil, availableDisks)
}
