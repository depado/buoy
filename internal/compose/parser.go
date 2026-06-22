package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var DefaultPatterns = []string{
	"compose.y*ml",
	"docker-compose.y*ml",
}

type VolumeEntry struct {
	Service     string `json:"service"`
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	Resolved    string `json:"resolved,omitempty"`
}

type ServiceInfo struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	Volumes []VolumeEntry     `json:"volumes"`
}

type StackInfo struct {
	Path     string        `json:"path"`
	Services []ServiceInfo `json:"services"`
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Labels  any   `yaml:"labels"`
	Volumes []any `yaml:"volumes"`
}

func Discover(dir string, maxDepth int, patterns []string) ([]StackInfo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve directory: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", abs)
	}

	var stacks []StackInfo

	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if !d.IsDir() {
			return nil
		}

		if maxDepth >= 0 && path != abs {
			rel, _ := filepath.Rel(abs, path)
			depth := len(strings.Split(rel, string(os.PathSeparator)))
			if depth > maxDepth {
				return filepath.SkipDir
			}
		}

		composePath, ferr := findComposeFile(path, patterns)
		if ferr != nil {
			return nil //nolint:nilerr
		}

		//nolint:gosec
		data, rerr := os.ReadFile(composePath)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", composePath, rerr)
			return nil
		}

		services, perr := parseCompose(composePath, data)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", composePath, perr)
			return nil
		}

		stacks = append(stacks, StackInfo{
			Path:     composePath,
			Services: services,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	if len(stacks) == 0 {
		return nil, fmt.Errorf("no compose files found in %s (looked up to depth %d)", abs, maxDepth)
	}

	return stacks, nil
}

func findComposeFile(dir string, patterns []string) (string, error) {
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			continue
		}
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no compose file found in %s (patterns: %s)", dir, strings.Join(patterns, ", "))
}

func parseCompose(path string, data []byte) ([]ServiceInfo, error) {
	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("invalid compose YAML: %w", err)
	}

	baseDir := filepath.Dir(path)
	var services []ServiceInfo

	for svcName, svc := range cf.Services {
		si := ServiceInfo{
			Name:   svcName,
			Labels: normalizeLabels(svc.Labels),
		}
		for _, vol := range svc.Volumes {
			entry, ok := parseVolumeEntry(svcName, vol, baseDir)
			if ok {
				si.Volumes = append(si.Volumes, entry)
			}
		}
		services = append(services, si)
	}

	return services, nil
}

func parseVolumeEntry(svcName string, vol any, baseDir string) (VolumeEntry, bool) {
	switch v := vol.(type) {
	case string:
		return parseShortSyntax(svcName, v, baseDir)
	case map[string]any:
		return parseLongSyntax(svcName, v, baseDir)
	default:
		return VolumeEntry{}, false
	}
}

func parseShortSyntax(svcName, spec string, baseDir string) (VolumeEntry, bool) {
	parts := splitVolumeSpec(spec)
	if len(parts) < 2 {
		return VolumeEntry{}, false
	}

	source := parts[0]
	target := parts[1]
	mode := "rw"
	if len(parts) == 3 {
		switch parts[2] {
		case "ro":
			mode = "ro"
		case "rw":
			mode = "rw"
		}
	}

	volType, resolved := classifySource(source, baseDir)
	return makeVolumeEntry(svcName, volType, source, resolved, target, mode), true
}

func parseLongSyntax(svcName string, m map[string]any, baseDir string) (VolumeEntry, bool) {
	volType, _ := m["type"].(string)
	source, _ := m["source"].(string)
	target, _ := m["target"].(string)

	if volType == "" || source == "" || target == "" {
		return VolumeEntry{}, false
	}

	mode := "rw"
	if readOnly, ok := m["read_only"].(bool); ok && readOnly {
		mode = "ro"
	}

	resolved := source
	if volType == "bind" {
		resolved = resolvePath(resolveVars(source), baseDir)
	}

	return makeVolumeEntry(svcName, volType, source, resolved, target, mode), true
}

func makeVolumeEntry(svcName, volType, source, resolved, target, mode string) VolumeEntry {
	entry := VolumeEntry{
		Service:     svcName,
		Type:        volType,
		Source:      source,
		Destination: target,
		Mode:        mode,
	}
	if volType == "bind" {
		entry.Resolved = resolved
	}
	return entry
}

var varPattern = regexp.MustCompile(`\$\{(\w+)(?::[?+-]([^}]*))?\}`)

func classifySource(source, baseDir string) (volType, resolved string) {
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") || strings.ContainsRune(source, '/') || strings.ContainsRune(source, '$') {
		return "bind", resolvePath(resolveVars(source), baseDir)
	}
	return "volume", source
}

func resolveVars(s string) string {
	return varPattern.ReplaceAllStringFunc(s, func(m string) string {
		parts := varPattern.FindStringSubmatch(m)
		if len(parts) == 3 && parts[2] != "" {
			return parts[2]
		}
		return m
	})
}

func splitVolumeSpec(spec string) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	inVar := false

	for i := 0; i < len(spec); i++ {
		ch := spec[i]
		if ch == '$' && i+1 < len(spec) && spec[i+1] == '{' {
			inVar = true
			depth = 1
			current.WriteByte(ch)
			current.WriteByte(spec[i+1])
			i++
			continue
		}
		if inVar {
			current.WriteByte(ch)
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					inVar = false
				}
			}
			continue
		}
		if ch == ':' && len(parts) < 2 {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, current.String())
	return parts
}

func resolvePath(path, baseDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

func normalizeLabels(raw any) map[string]string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		result := make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				result[k] = s
			}
		}
		return result
	case []any:
		result := make(map[string]string)
		for _, item := range v {
			if s, ok := item.(string); ok {
				k, val, found := strings.Cut(s, "=")
				if found {
					result[k] = val
				}
			}
		}
		return result
	}
	return nil
}
