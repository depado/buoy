package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/depado/gorich"
	"github.com/depado/gorich/style"
	"github.com/depado/gorich/table"
	"github.com/depado/gorich/table/box"
	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/compose"
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

		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(stacks)
		}

		mounts, hasVolumes, _ := scanStacks(stacks)
		renderSuggestions(mounts, hasVolumes)

		return nil
	},
}

// volumelessService is an enabled service that has no backup-eligible volumes.
type volumelessService struct {
	stack   string
	service string
}

// scanStacks renders one table per stack and collects bind mount sources,
// named-volume presence, and services without backup-eligible volumes.
func scanStacks(stacks []compose.StackInfo) (bindMounts map[string]bool, hasVolumes bool, volumeless []volumelessService) {
	bindMounts = make(map[string]bool)
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

		var stackEmpty []volumelessService

		for _, s := range stack.Services {
			cfg := types.ParseBackupConfig(s.Labels, "", "")

			volumeCount := 0
			for _, e := range s.Volumes {
				if _, ok := types.MountMatches(types.Mount{Source: e.Source, Destination: e.Destination}, cfg.Include, cfg.Exclude); !ok {
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
					switch e.Type {
					case "bind":
						bindMounts[e.Resolved] = true
					case "volume":
						hasVolumes = true
					}
				}
			}

			if cfg.Enabled && volumeCount == 0 {
				stackEmpty = append(stackEmpty, volumelessService{stack: stack.Path, service: s.Name})
			}
		}

		for _, v := range stackEmpty {
			tbl.AddRow(v.service, "[green]yes[/]", "[yellow](none)[/]", "-", "-", "-")
			volumeless = append(volumeless, v)
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
		for _, v := range volumeless {
			tbl.AddRow(v.stack, v.service)
		}
		c.Render(tbl)
	}

	return
}

// renderSuggestions prints the exact mount list and, when a common ancestor
// exists, the collapsed alternative.
func renderSuggestions(bindMounts map[string]bool, hasVolumes bool) {
	needsDockerVolumes := hasVolumes && !bindMounts["/var/lib/docker/volumes"]
	if len(bindMounts) == 0 && !needsDockerVolumes {
		return
	}

	mounts := sortedKeys(bindMounts)
	if needsDockerVolumes {
		mounts = append(mounts, "/var/lib/docker/volumes")
		sort.Strings(mounts)
	}

	fmt.Println()
	gorich.Println("[bold]Add to buoy's compose service volumes:[/]")
	printMounts(mounts)

	if collapsed := collapseMounts(mounts); !slices.Equal(collapsed, mounts) {
		fmt.Println()
		gorich.Println("[bold]Suggested collapsed mounts (alternative to the above):[/]")
		printMounts(collapsed)
	}
}

func printMounts(mounts []string) {
	fmt.Println("volumes:")
	for _, src := range mounts {
		fmt.Printf("  - %s:%s:ro\n", src, src)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collapseMounts shortens a set of mount sources to their common path
// ancestors. When several paths share a common ancestor, mounting that
// ancestor read-only covers them all: e.g. "${DOCKERDATA_DIR:-.}/beszel"
// and "${DOCKERDATA_DIR:-.}/gotify" collapse to "${DOCKERDATA_DIR:-.}".
// A path without siblings keeps its full form.
func collapseMounts(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	remaining := append([]string(nil), paths...)
	out := make([]string, 0, len(remaining))

	for len(remaining) > 0 {
		best, bestCover, bestLen := "", 0, 0
		for i := 0; i < len(remaining); i++ {
			for j := i + 1; j < len(remaining); j++ {
				anc := longestCommonPathPrefix(remaining[i], remaining[j])
				if anc == "" || anc == "/" {
					continue
				}
				cover := 0
				for _, p := range remaining {
					if isUnder(p, anc) {
						cover++
					}
				}
				l := len(strings.Split(anc, "/"))
				if cover > bestCover || (cover == bestCover && l > bestLen) {
					best, bestCover, bestLen = anc, cover, l
				}
			}
		}
		if bestCover < 2 {
			out = append(out, remaining...)
			break
		}
		out = append(out, best)
		var kept []string
		for _, p := range remaining {
			if !isUnder(p, best) {
				kept = append(kept, p)
			}
		}
		remaining = kept
	}
	sort.Strings(out)
	return out
}

// longestCommonPathPrefix returns the longest common ancestor directory of
// two paths, compared segment by segment.
func longestCommonPathPrefix(a, b string) string {
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	var out []string
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			break
		}
		out = append(out, as[i])
	}
	return strings.Join(out, "/")
}

// isUnder reports whether path equals ancestor or is nested below it.
func isUnder(path, ancestor string) bool {
	return path == ancestor || strings.HasPrefix(path, ancestor+"/")
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
