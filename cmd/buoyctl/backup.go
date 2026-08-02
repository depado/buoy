package main

import (
	"fmt"

	"github.com/depado/gorich"
	"github.com/depado/gorich/style"
	"github.com/depado/gorich/table"
	"github.com/depado/gorich/table/box"
	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/types"
)

var backupCmd = &cobra.Command{
	Use:   "backup [containers...]",
	Short: "Trigger backup for one or more containers",
	Long: `Trigger an immediate backup.

  buoyctl backup uptime-kuma beszel       # specific containers by name
  buoyctl backup --project myapp          # all services in a compose project
  buoyctl backup --project myapp db api   # specific services in a project
  buoyctl backup --all                    # all scheduled containers`,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		project, _ := cmd.Flags().GetString("project")

		if all {
			return runAllBackup(cmd)
		}
		if project != "" {
			return runProjectBackup(cmd, project, args)
		}
		if len(args) == 0 {
			return fmt.Errorf("specify container names, --project, or --all")
		}
		return runContainerBackup(cmd, args)
	},
}

func runContainerBackup(cmd *cobra.Command, containers []string) error {
	results, err := withSpinner(cmd, "Triggering backup...", func() ([]types.BackupResult, error) {
		return clientAPI().TriggerBackup(containers)
	})
	if err != nil {
		return fmt.Errorf("trigger backup: %w", err)
	}
	return renderBackupResults(cmd, results)
}

func runAllBackup(cmd *cobra.Command) error {
	results, err := withSpinner(cmd, "Triggering backup for all containers...", func() ([]types.BackupResult, error) {
		return clientAPI().TriggerBackupAll()
	})
	if err != nil {
		return fmt.Errorf("trigger backup: %w", err)
	}
	return renderBackupResults(cmd, results)
}

func runProjectBackup(cmd *cobra.Command, project string, services []string) error {
	result, err := withSpinner(cmd, "Triggering project backup...", func() (*types.BackupResult, error) {
		return clientAPI().TriggerProjectBackup(project, services)
	})
	if err != nil {
		return fmt.Errorf("trigger backup: %w", err)
	}
	return renderBackupResults(cmd, []types.BackupResult{*result})
}

func renderBackupResults(cmd *cobra.Command, results []types.BackupResult) error {
	if len(results) == 0 {
		fmt.Println("No containers backed up.")
		return nil
	}

	if asJSON(cmd) {
		return printJSON(results)
	}

	tbl := table.NewTableWithOptions(
		[]string{"Container", "Status"},
		table.WithBox(box.SIMPLE),
		table.WithHeaderStyle(&style.Bold),
	)
	failures := 0
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = fmt.Sprintf("FAIL: %s", r.Error)
			failures++
		}
		tbl.AddRow(r.Container, status)
	}
	gorich.Console().Render(tbl)
	if failures > 0 {
		return fmt.Errorf("%d/%d backups failed", failures, len(results))
	}
	return nil
}

func setupBackupCommand() {
	backupCmd.Flags().Bool("all", false, "trigger backup for all scheduled containers")
	backupCmd.Flags().String("project", "", "scope positional args to services in a compose project")
	backupCmd.Flags().Bool("json", false, "output as JSON")
	backupCmd.MarkFlagsMutuallyExclusive("all", "project")
}
