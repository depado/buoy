package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/api"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage tracked restic repositories",
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		entries, err := client.ListRepos(orphaned)
		if err != nil {
			return fmt.Errorf("list repos: %w", err)
		}
		if len(entries) == 0 {
			fmt.Println("No repositories found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "URL\tCONTAINER\tPROJECT\tSERVICE\tLAST BACKUP\tOK\tORPHANED")
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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				e.URL, e.ContainerName, e.ComposeProject, e.ComposeService, backupAt, ok, orphaned)
		}
		w.Flush()
		return nil
	},
}

var repoCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run restic check on all known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		readData, _ := cmd.Flags().GetBool("read-data")
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		results, err := client.CheckRepos(readData, orphaned)
		if err != nil {
			return fmt.Errorf("check repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to check.")
			return nil
		}

		failures := 0
		for _, r := range results {
			if r.OK {
				fmt.Printf("OK    %s\n", r.Repo)
			} else {
				fmt.Printf("FAIL  %s: %s\n", r.Repo, r.Error)
				failures++
			}
		}
		fmt.Printf("\n%d/%d repos ok\n", len(results)-failures, len(results))
		if failures > 0 {
			return fmt.Errorf("%d repo(s) failed check", failures)
		}
		return nil
	},
}

var repoStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show aggregate storage statistics across all repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		stats, err := client.StatsRepos(orphaned)
		if err != nil {
			return fmt.Errorf("stats repos: %w", err)
		}

		if stats.Total != nil {
			fmt.Printf("Total repos: %d\n", stats.Total.SnapshotsCount)
			fmt.Printf("Total size:  %s\n", formatBytes(stats.Total.TotalSize))
			fmt.Printf("Total files: %d\n", stats.Total.TotalFileCount)
			fmt.Printf("Total blobs: %d\n", stats.Total.TotalBlobCount)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\nREPO\tSIZE\tFILES\tBLOBS\tSNAPSHOTS")
		for _, r := range stats.Repos {
			if r.Error != "" {
				fmt.Fprintf(w, "%s\tERROR: %s\n", r.Repo, r.Error)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n",
				r.Repo,
				formatBytes(r.Stats.TotalSize),
				r.Stats.TotalFileCount,
				r.Stats.TotalBlobCount,
				r.Stats.SnapshotsCount,
			)
		}
		w.Flush()
		return nil
	},
}

var repoUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock all known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		results, err := client.UnlockRepos(orphaned)
		if err != nil {
			return fmt.Errorf("unlock repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to unlock.")
			return nil
		}

		failures := 0
		for _, r := range results {
			if r.OK {
				fmt.Printf("OK    %s\n", r.Repo)
			} else {
				fmt.Printf("FAIL  %s: %s\n", r.Repo, r.Error)
				failures++
			}
		}
		fmt.Printf("\n%d/%d repos unlocked\n", len(results)-failures, len(results))
		if failures > 0 {
			return fmt.Errorf("%d repo(s) failed unlock", failures)
		}
		return nil
	},
}

var repoForgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "Apply retention policy to all known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		retention, _ := cmd.Flags().GetString("retention")
		if retention == "" {
			return fmt.Errorf("--retention is required (e.g. keep-daily:7,keep-weekly:4)")
		}
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		results, err := client.ForgetRepos(retention, orphaned)
		if err != nil {
			return fmt.Errorf("forget repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to forget.")
			return nil
		}

		failures := 0
		for _, r := range results {
			if r.OK {
				fmt.Printf("OK    %s\n", r.Repo)
			} else {
				fmt.Printf("FAIL  %s: %s\n", r.Repo, r.Error)
				failures++
			}
		}
		fmt.Printf("\n%d/%d repos forgot\n", len(results)-failures, len(results))
		if failures > 0 {
			return fmt.Errorf("%d repo(s) failed forget", failures)
		}
		return nil
	},
}

var repoPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune all known repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		orphaned, _ := cmd.Flags().GetBool("orphaned")
		client := api.DefaultClient()
		if url, _ := cmd.Flags().GetString("api.url"); url != "" {
			client.BaseURL = url
		}
		if token, _ := cmd.Flags().GetString("api.token"); token != "" {
			client.Token = token
		}

		results, err := client.PruneRepos(orphaned)
		if err != nil {
			return fmt.Errorf("prune repos: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No repositories to prune.")
			return nil
		}

		failures := 0
		for _, r := range results {
			if r.OK {
				fmt.Printf("OK    %s\n", r.Repo)
			} else {
				fmt.Printf("FAIL  %s: %s\n", r.Repo, r.Error)
				failures++
			}
		}
		fmt.Printf("\n%d/%d repos pruned\n", len(results)-failures, len(results))
		if failures > 0 {
			return fmt.Errorf("%d repo(s) failed prune", failures)
		}
		return nil
	},
}

func setupRepoCommands() {
	repoListCmd.Flags().Bool("orphaned", false, "show only orphaned repositories")
	repoCheckCmd.Flags().Bool("orphaned", false, "check only orphaned repositories")
	repoCheckCmd.Flags().Bool("read-data", false, "read all pack files for full data integrity check")
	repoUnlockCmd.Flags().Bool("orphaned", false, "unlock orphaned repositories")
	repoForgetCmd.Flags().Bool("orphaned", false, "forget only orphaned repositories")
	repoForgetCmd.Flags().String("retention", "", "retention policy (e.g. keep-daily:7,keep-weekly:4)")
	repoStatsCmd.Flags().Bool("orphaned", false, "show stats for orphaned repositories only")
	repoPruneCmd.Flags().Bool("orphaned", false, "prune only orphaned repositories")

	for _, c := range []*cobra.Command{repoListCmd, repoCheckCmd, repoStatsCmd, repoUnlockCmd, repoForgetCmd, repoPruneCmd} {
		c.Flags().String("api.url", "", "buoy API URL (defaults to BUOY_URL env or http://127.0.0.1:8080)")
		c.Flags().String("api.token", "", "buoy API bearer token (defaults to BUOY_TOKEN env)")
		repoCmd.AddCommand(c)
	}
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
