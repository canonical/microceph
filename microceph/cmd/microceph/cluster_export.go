package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterExport struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterExport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Generates a json dump of cluster state",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterExport) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	query := "select * from config"
	dump, batch, err := m.SQL(context.Background(), query)
	if err != nil {
		return err
	}

	if dump != "" {
		fmt.Print(dump)
		return nil
	}

	for i, result := range batch.Results {
		if len(batch.Results) > 1 {
			fmt.Printf("=> Query %d:\n\n", i)
		}

		if result.Type == "select" {
			jsonOut, _ := json.Marshal(getConfigDict(result.Rows))
			fmt.Printf("%s\n", jsonOut)
		} else {
			fmt.Printf("Rows affected: %d\n", result.RowsAffected)
		}

		if len(batch.Results) > 1 {
			fmt.Printf("\n")
		}
	}
	return nil
}

func getConfigDict(rows [][]any) map[string]string {
	dict := map[string]string{}

	for _, row := range rows {
		// 0: index, 1: key, 2: value
		dict[fmt.Sprintf("%v", row[1])] = fmt.Sprintf("%v", row[2])
	}

	return dict
}
