package backup

import (
	"strings"

	"github.com/depado/buoy/internal/docker"
)

type depInfo struct {
	Name      string
	Condition string
}

// orderForStopFromDeps returns service names in reverse dependency order
// using a pre-computed dependency map.
func orderForStopFromDeps(deps map[string][]depInfo, warn func(string)) []string {
	return reverseTopological(deps, warn)
}

// orderForStartFromDeps returns service names in dependency order
// using a pre-computed dependency map.
func orderForStartFromDeps(deps map[string][]depInfo, warn func(string)) []string {
	return topological(deps, warn)
}

// serviceDeps extracts the depends_on graph from container labels.
// Returns map of service name → list of (dependency name, condition).
func serviceDeps(ctrs []*docker.Container) map[string][]depInfo {
	deps := make(map[string][]depInfo)
	for _, ctr := range ctrs {
		svc := ctr.ComposeService
		if svc == "" {
			continue
		}
		label := ctr.Labels["com.docker.compose.depends_on"]
		if label == "" {
			continue
		}
		for _, part := range strings.Split(label, ",") {
			part = strings.TrimSpace(part)
			parts := strings.SplitN(part, ":", 3)
			if len(parts) == 0 || parts[0] == "" {
				continue
			}
			info := depInfo{Name: parts[0], Condition: "service_started"}
			if len(parts) > 1 && parts[1] != "" {
				info.Condition = parts[1]
			}
			deps[svc] = append(deps[svc], info)
		}
	}
	return deps
}

// depConditions returns the full dependency list for a service.
func depConditions(ctrs []*docker.Container, serviceName string) []depInfo {
	return serviceDeps(ctrs)[serviceName]
}

// depConditionsFrom looks up the dependency list for a service from a
// pre-computed dependency map. Prefer this over depConditions when the
// caller already has the deps map from serviceDeps to avoid redundant
// label parsing.
func depConditionsFrom(deps map[string][]depInfo, serviceName string) []depInfo {
	return deps[serviceName]
}

// topological returns service names sorted so that dependencies come first.
func topological(deps map[string][]depInfo, warn func(string)) []string {
	depNames := make(map[string][]string)
	for svc, infos := range deps {
		for _, info := range infos {
			depNames[svc] = append(depNames[svc], info.Name)
		}
	}
	return topologicalSort(depNames, warn)
}

// reverseTopological returns service names in reverse dependency order.
func reverseTopological(deps map[string][]depInfo, warn func(string)) []string {
	order := topological(deps, warn)
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

func topologicalSort(deps map[string][]string, warn func(string)) []string {
	all := allServices(deps)
	visited := make(map[string]bool)
	temp := make(map[string]bool)
	var result []string

	var visit func(string)
	visit = func(node string) {
		if visited[node] {
			return
		}
		if temp[node] {
			if warn != nil {
				warn("dependency cycle detected involving service: " + node)
			}
			return
		}
		temp[node] = true
		for _, dep := range deps[node] {
			visit(dep)
		}
		temp[node] = false
		visited[node] = true
		result = append(result, node)
	}

	for _, node := range all {
		if !visited[node] {
			visit(node)
		}
	}
	return result
}

func allServices(deps map[string][]string) []string {
	seen := make(map[string]bool)
	var result []string
	for svc := range deps {
		if !seen[svc] {
			seen[svc] = true
			result = append(result, svc)
		}
	}
	for _, ds := range deps {
		for _, d := range ds {
			if !seen[d] {
				seen[d] = true
				result = append(result, d)
			}
		}
	}
	return result
}

// serviceContainers groups containers by their compose service name.
func serviceContainers(ctrs []*docker.Container) map[string][]*docker.Container {
	groups := make(map[string][]*docker.Container)
	for _, ctr := range ctrs {
		svc := ctr.ComposeService
		groups[svc] = append(groups[svc], ctr)
	}
	return groups
}

// stopSet returns the set of service names that must be stopped.
// Only batch containers contribute to the stop decision via stop_before_backup.
// The dependency graph is built from all containers so cascade works correctly.
func stopSet(batch []*docker.Container, all []*docker.Container) map[string]bool {
	dependents := buildDependents(all)
	stop := make(map[string]bool)

	for _, ctr := range batch {
		cfg := docker.ParseBackupConfig(ctr.Labels, "", "")
		if !cfg.StopBefore {
			continue
		}
		svc := ctr.ComposeService
		if svc == "" {
			continue
		}
		addDependents(svc, stop, dependents)
	}

	return stop
}

// buildDependents builds a reverse dependency map: service → services that depend on it.
func buildDependents(ctrs []*docker.Container) map[string][]string {
	dependents := make(map[string][]string)
	deps := serviceDeps(ctrs)

	for svc, depInfos := range deps {
		for _, info := range depInfos {
			dependents[info.Name] = append(dependents[info.Name], svc)
		}
	}
	return dependents
}

func addDependents(svc string, stop map[string]bool, dependents map[string][]string) {
	stop[svc] = true
	for _, dep := range dependents[svc] {
		if !stop[dep] {
			addDependents(dep, stop, dependents)
		}
	}
}
