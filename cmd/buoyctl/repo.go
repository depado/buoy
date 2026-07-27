package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/depado/gorich"
	"github.com/depado/gorich/live"
	"github.com/depado/gorich/style"
	"github.com/depado/gorich/table"
	"github.com/depado/gorich/table/box"
	"github.com/spf13/cobra"

	"github.com/depado/buoy/client"
	"github.com/depado/buoy/internal/types"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage tracked restic repositories",
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		entries, err := withSpinner(cmd, "Fetching repos...", func() ([]types.RepoEntry, error) {
			return clientAPI().ListRepos(repo, orphaned)
		})
		if err != nil {
			return fmt.Errorf("list repos: %w", err)
		}
		if len(entries) == 0 {
			fmt.Println("No repositories found.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(entries)
		}

		tbl := table.NewTableWithOptions(
			[]string{"URL", "Container", "Project", "Service", "Last Backup", "OK", "Orphaned"},
			table.WithBox(box.SIMPLE),
			table.WithHeaderStyle(&style.Bold),
		)
		for _, e := range entries {
			backupAt := "-"
			if !e.LastBackupAt.IsZero() {
				backupAt = e.LastBackupAt.Format(time.RFC3339)
			}
			ok := "-"
			if !e.LastBackupAt.IsZero() {
				ok = fmt.Sprintf("%v", e.LastBackupOK)
			}
			orphaned := "no"
			if e.Orphaned {
				orphaned = "yes"
			}
			tbl.AddRow(e.URL, e.ContainerName, e.ComposeProject, e.ComposeService, backupAt, ok, orphaned)
		}
		gorich.Console().Render(tbl)
		return nil
	},
}

var repoCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run restic check on known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		readData, _ := cmd.Flags().GetBool("read-data")
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		results, err := withSpinner(cmd, "Running check...", func() ([]client.Result, error) {
			return clientAPI().CheckRepos(repo, readData, orphaned)
		})
		if err != nil {
			return fmt.Errorf("check repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to check.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(results)
		}
		return printOpResults(results, "ok")
	},
}

var repoStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show storage statistics across repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		stats, err := withSpinner(cmd, "Fetching stats...", func() (*client.StatsResponse, error) {
			return clientAPI().StatsRepos(repo, orphaned)
		})
		if err != nil {
			return fmt.Errorf("stats repos: %w", err)
		}

		if asJSON(cmd) {
			return printJSON(stats)
		}

		c := gorich.Console()

		if stats.Total != nil {
			summary := table.NewTableWithOptions(nil,
				table.WithBox(box.SIMPLE),
				table.WithHeaderStyle(&style.Bold),
			)
			summary.AddColumn("Metric")
			summary.AddColumn("Value")
			summary.AddRow("[bold]Total repos[/]", len(stats.Repos))
			summary.AddRow("[bold]Total snapshots[/]", stats.Total.SnapshotsCount)
			summary.AddRow("[bold]Total size[/]", formatBytes(stats.Total.TotalSize))
			summary.AddRow("[bold]Total files[/]", stats.Total.TotalFileCount)
			summary.AddRow("[bold]Total blobs[/]", stats.Total.TotalBlobCount)
			if stats.Total.TotalUncompressedSize > 0 {
				summary.AddRow("[bold]Uncompressed[/]", formatBytes(stats.Total.TotalUncompressedSize))
			}
			c.Render(summary)
		}

		tbl := table.NewTableWithOptions(
			[]string{"Repo", "Size", "Uncompressed", "Ratio", "Files", "Blobs", "Snapshots"},
			table.WithBox(box.SIMPLE),
			table.WithHeaderStyle(&style.Bold),
		)
		for _, r := range stats.Repos {
			if r.Error != "" {
				tbl.AddRow(r.Repo, fmt.Sprintf("ERROR: %s", r.Error))
				continue
			}
			uc := "-"
			ratio := ""
			if r.Stats.TotalUncompressedSize > 0 {
				uc = formatBytes(r.Stats.TotalUncompressedSize)
				if r.Stats.CompressionRatio > 0 {
					ratio = fmt.Sprintf("%.1fx", r.Stats.CompressionRatio)
				}
			}
			tbl.AddRow(
				r.Repo,
				formatBytes(r.Stats.TotalSize),
				uc,
				ratio,
				r.Stats.TotalFileCount,
				r.Stats.TotalBlobCount,
				r.Stats.SnapshotsCount,
			)
		}
		c.Render(tbl)
		return nil
	},
}

var repoUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireScope(cmd); err != nil {
			return err
		}
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		results, err := withSpinner(cmd, "Unlocking repos...", func() ([]client.Result, error) {
			return clientAPI().UnlockRepos(repo, orphaned)
		})
		if err != nil {
			return fmt.Errorf("unlock repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to unlock.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(results)
		}
		return printOpResults(results, "unlocked")
	},
}

var repoForgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "Apply retention policy to known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		retention, _ := cmd.Flags().GetString("retention")
		if retention == "" {
			return fmt.Errorf("--retention is required (e.g. keep-daily:7,keep-weekly:4)")
		}
		if err := requireScope(cmd); err != nil {
			return err
		}
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		results, err := withSpinner(cmd, "Forgetting snapshots...", func() ([]client.Result, error) {
			return clientAPI().ForgetRepos(repo, retention, orphaned)
		})
		if err != nil {
			return fmt.Errorf("forget repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to forget.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(results)
		}
		return printOpResults(results, "forgot")
	},
}

var repoPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireScope(cmd); err != nil {
			return err
		}
		orphaned, err := orphanedFilter(cmd)
		if err != nil {
			return err
		}
		repo := getRepoFlag(cmd)

		results, err := withSpinner(cmd, "Pruning repos...", func() ([]client.Result, error) {
			return clientAPI().PruneRepos(repo, orphaned)
		})
		if err != nil {
			return fmt.Errorf("prune repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to prune.")
			return nil
		}

		if asJSON(cmd) {
			return printJSON(results)
		}
		return printOpResults(results, "pruned")
	},
}

func setupRepoCommands() {
	addRepoFlags(repoListCmd, "show")
	addRepoFlags(repoCheckCmd, "check")
	addRepoFlags(repoUnlockCmd, "unlock")
	addRepoFlags(repoForgetCmd, "forget")
	addRepoFlags(repoStatsCmd, "show stats for")
	addRepoFlags(repoPruneCmd, "prune")

	repoCheckCmd.Flags().Bool("read-data", false, "read all pack files for full data integrity check")
	repoForgetCmd.Flags().String("retention", "", "retention policy (e.g. keep-daily:7,keep-weekly:4)")

	for _, c := range []*cobra.Command{repoListCmd, repoCheckCmd, repoStatsCmd, repoUnlockCmd, repoForgetCmd, repoPruneCmd} {
		c.Flags().Bool("json", false, "output as JSON")
		repoCmd.AddCommand(c)
	}
}

func addRepoFlags(cmd *cobra.Command, verb string) {
	cmd.Flags().Bool("orphaned", false, verb+" orphaned repositories only")
	cmd.Flags().Bool("all", false, verb+" all repositories including orphaned")
	cmd.Flags().String("repo", "", verb+" a specific repository")
}

func getRepoFlag(cmd *cobra.Command) string {
	repo, _ := cmd.Flags().GetString("repo")
	return repo
}

func requireScope(cmd *cobra.Command) error {
	repo, _ := cmd.Flags().GetString("repo")
	orphaned, _ := cmd.Flags().GetBool("orphaned")
	all, _ := cmd.Flags().GetBool("all")
	if repo == "" && !orphaned && !all {
		return fmt.Errorf("must specify --repo, --orphaned, or --all")
	}
	return nil
}

func orphanedFilter(cmd *cobra.Command) (client.OrphanedFilter, error) {
	orphaned, _ := cmd.Flags().GetBool("orphaned")
	all, _ := cmd.Flags().GetBool("all")
	if orphaned && all {
		return "", fmt.Errorf("--orphaned and --all are mutually exclusive")
	}
	switch {
	case orphaned:
		return client.Orphaned, nil
	case all:
		return client.AllRepos, nil
	default:
		return client.NonOrphaned, nil
	}
}

func clientAPI() *client.Client {
	return client.NewClient(
		v.GetString("api.url"),
		v.GetString("api.token"),
	)
}

func asJSON(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func withSpinner[T any](cmd *cobra.Command, text string, fn func() (T, error)) (T, error) {
	if asJSON(cmd) {
		return fn()
	}
	s := live.StartSpinner("[bold]" + text + "[/]")
	result, err := fn()
	s.Stop()
	return result, err
}

func printOpResults(results []client.Result, opName string) error {
	failures := 0
	for _, r := range results {
		if r.OK {
			fmt.Printf("OK    %s\n", r.Repo)
		} else {
			fmt.Printf("FAIL  %s: %s\n", r.Repo, r.Error)
			failures++
		}
	}
	fmt.Printf("\n%d/%d repos %s\n", len(results)-failures, len(results), opName)
	if failures > 0 {
		return fmt.Errorf("%d repo(s) failed %s", failures, opName)
	}
	return nil
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
