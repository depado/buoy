package main

import (
	"fmt"
	"strings"

	"github.com/depado/gorich"
	"github.com/depado/gorich/style"
	"github.com/depado/gorich/table"
	"github.com/depado/gorich/table/box"
	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/types"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List currently scheduled backups",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := withSpinner(cmd, "Fetching scheduled backups...", func() ([]types.APIScheduledEntry, error) {
			return clientAPI().ListScheduled()
		})
		if err != nil {
			return fmt.Errorf("list scheduled: %w", err)
		}

		if len(entries) == 0 {
			fmt.Println("No scheduled backups.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(entries)
		}

		tbl := table.NewTableWithOptions(
			[]string{"Container", "Project", "Service", "Schedule", "Repos", "Stop"},
			table.WithBox(box.SIMPLE),
			table.WithHeaderStyle(&style.Bold),
		)
		for _, e := range entries {
			repos := "-"
			if len(e.Repos) > 0 {
				urls := make([]string, len(e.Repos))
				for i, r := range e.Repos {
					marker := "?"
					if r.Created {
						marker = "✓"
					}
					urls[i] = fmt.Sprintf("%s %s", marker, r.URL)
				}
				repos = strings.Join(urls, "\n")
			}
			stop := "no"
			if e.StopBefore {
				stop = "yes"
			}
			tbl.AddRow(e.ContainerName, e.ComposeProject, e.ComposeService, e.Schedule, repos, stop)
		}
		gorich.Console().Render(tbl)
		return nil
	},
}

func setupListCommand() {
	listCmd.Flags().Bool("json", false, "output as JSON")
}
