package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/depado/gorich"
	"github.com/depado/gorich/style"
	"github.com/depado/gorich/table"
	"github.com/depado/gorich/table/box"
	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/compose"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/types"
)

var discoverCmd = &cobra.Command{
	Use:   "discover [directory]",
	Short: "Scan compose files for volumes and bind mounts needed by buoy",
	Long: `Scan a directory recursively for Docker Compose files and list all volumes
and bind mounts that buoy would need access to for backing up the services.

Respects buoy labels (buoy.enabled, buoy.include, buoy.exclude) defined
in the compose file. Only mounts from enabled services are included in the
host paths and YAML sections.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}

		depth, _ := cmd.Flags().GetInt("depth")
		patternStr, _ := cmd.Flags().GetString("pattern")
		resolveEnv, _ := cmd.Flags().GetBool("resolve-env")
		patterns := types.SplitTrim(patternStr)
		if len(patterns) == 0 {
			patterns = compose.DefaultPatterns
		}

		stacks, err := compose.Discover(dir, depth, patterns, resolveEnv)
		if err != nil {
			return err
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(stacks)
		}

		bindMounts := make(map[string]bool)
		hasVolumes := false
		var volumeless []string
		c := gorich.Console()

		for _, stack := range stacks {
			tbl := table.NewTableWithOptions(nil,
				table.WithBox(box.SIMPLE),
				table.WithHeaderStyle(&style.Bold),
				table.WithTitle("[green]"+stack.Path+"[/]"),
			)
			tbl.AddColumn("Service")
			tbl.AddColumn("Enabled")
			tbl.AddColumn("Type")
			tbl.AddColumn("Source")
			tbl.AddColumn("Destination")
			tbl.AddColumn("Mode")

			var stackEmpty []string

			for _, s := range stack.Services {
				cfg := docker.ParseBackupConfig(s.Labels, "", "")

				volumeCount := 0
				for _, e := range s.Volumes {
					if isExcluded(e, cfg) {
						continue
					}
					volumeCount++

					enabled := "[red]no[/]"
					if cfg.Enabled {
						enabled = "[green]yes[/]"
					}

					source := e.Source
					if cfg.Enabled && e.Type == "bind" && !isBuiltinMount(e.Source) {
						source = "[bold green]" + e.Source + "[/]"
					}

					tbl.AddRow(e.Service, enabled, e.Type, source, e.Destination, e.Mode)

					if cfg.Enabled {
						if e.Type == "bind" {
							bindMounts[e.Resolved] = true
						}
						if e.Type == "volume" {
							hasVolumes = true
						}
					}
				}

				if cfg.Enabled && volumeCount == 0 {
					stackEmpty = append(stackEmpty, s.Name)
				}
			}

			for _, svc := range stackEmpty {
				tbl.AddRow(svc, "[green]yes[/]", "[yellow](none)[/]", "-", "-", "-")
				volumeless = append(volumeless, stack.Path+" / "+svc)
			}

			c.Render(tbl)
			c.Println()
		}

		if len(volumeless) > 0 {
			fmt.Println()
			tbl := table.NewTableWithOptions(nil,
				table.WithBox(box.SIMPLE),
				table.WithHeaderStyle(&style.Bold),
				table.WithTitle("[bold yellow]Enabled services with no volumes[/]"),
			)
			tbl.AddColumn("Stack")
			tbl.AddColumn("Service")
			for _, s := range volumeless {
				parts := strings.SplitN(s, " / ", 2)
				tbl.AddRow(parts[0], parts[1])
			}
			gorich.Console().Render(tbl)
		}

		needsDockerVolumes := hasVolumes && !bindMounts["/var/lib/docker/volumes"]

		if len(bindMounts) == 0 && !needsDockerVolumes {
			return nil
		}

		fmt.Println()
		gorich.Println("[bold]Add to buoy's compose service volumes:[/]")
		fmt.Println("volumes:")
		paths := sortedKeys(bindMounts)
		for _, src := range paths {
			fmt.Printf("  - %s:%s:ro\n", src, src)
		}
		if needsDockerVolumes {
			fmt.Println("  - /var/lib/docker/volumes:/var/lib/docker/volumes:ro")
		}

		return nil
	},
}

func isExcluded(e compose.VolumeEntry, cfg docker.BackupConfig) bool {
	if len(cfg.Include) > 0 {
		for _, entry := range cfg.Include {
			if entry.Key == e.Source || entry.Key == e.Destination {
				return false
			}
		}
		return true
	}
	for _, ex := range cfg.Exclude {
		if ex == e.Source || ex == e.Destination {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isBuiltinMount(source string) bool {
	switch source {
	case "/var/run/docker.sock", "/var/lib/docker/volumes":
		return true
	}
	return false
}

func setupDiscoverCommand() {
	discoverCmd.Flags().Int("depth", -1, "maximum directory depth to search for compose files (-1 for unlimited)")
	discoverCmd.Flags().String("pattern", "", "comma-separated glob patterns for compose files (default: compose.y*ml,docker-compose.y*ml)")
	discoverCmd.Flags().Bool("json", false, "output as JSON")
	discoverCmd.Flags().Bool("resolve-env", false, "resolve ${VAR} and ${VAR:-default} from .env files and process environment")
}
