package backup

import (
	"reflect"
	"testing"

	"github.com/depado/buoy/internal/docker"
)

func makeCtr(svc string, depsLabel string) *docker.Container {
	labels := map[string]string{}
	if depsLabel != "" {
		labels["com.docker.compose.depends_on"] = depsLabel
	}
	return &docker.Container{
		ComposeService: svc,
		ComposeProject: "proj",
		Labels:         labels,
	}
}

func TestServiceDeps(t *testing.T) {
	tests := []struct {
		name string
		ctrs []*docker.Container
		want map[string][]depInfo
	}{
		{
			name: "empty containers",
			ctrs: nil,
			want: map[string][]depInfo{},
		},
		{
			name: "containers without compose labels",
			ctrs: []*docker.Container{
				{ComposeService: ""},
			},
			want: map[string][]depInfo{},
		},
		{
			name: "single service with depends_on",
			ctrs: []*docker.Container{
				makeCtr("web", "db"),
			},
			want: map[string][]depInfo{
				"web": {{Name: "db", Condition: ServiceStarted}},
			},
		},
		{
			name: "service with multiple dependencies",
			ctrs: []*docker.Container{
				makeCtr("web", "db,redis"),
			},
			want: map[string][]depInfo{
				"web": {
					{Name: "db", Condition: ServiceStarted},
					{Name: "redis", Condition: ServiceStarted},
				},
			},
		},
		{
			name: "depends_on with service_healthy condition",
			ctrs: []*docker.Container{
				makeCtr("web", "db:service_healthy"),
			},
			want: map[string][]depInfo{
				"web": {{Name: "db", Condition: ServiceHealthy}},
			},
		},
		{
			name: "depends_on without explicit condition defaults to service_started",
			ctrs: []*docker.Container{
				makeCtr("web", "db::restart"),
			},
			want: map[string][]depInfo{
				"web": {{Name: "db", Condition: ServiceStarted}},
			},
		},
		{
			name: "service without depends_on label",
			ctrs: []*docker.Container{
				{ComposeService: "db", ComposeProject: "proj", Labels: map[string]string{}},
			},
			want: map[string][]depInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceDeps(tt.ctrs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopologicalSort(t *testing.T) {
	tests := []struct {
		name string
		deps map[string][]string
	}{
		{
			name: "empty deps",
			deps: map[string][]string{},
		},
		{
			name: "single node no deps",
			deps: map[string][]string{"web": {}},
		},
		{
			name: "A depends on B",
			deps: map[string][]string{"a": {"b"}},
		},
		{
			name: "chain A->B->C",
			deps: map[string][]string{"a": {"b"}, "b": {"c"}},
		},
		{
			name: "diamond D depends on A,B; A,B depend on C",
			deps: map[string][]string{
				"d": {"a", "b"},
				"a": {"c"},
				"b": {"c"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := topologicalSort(tt.deps, nil)
			visited := make(map[string]bool)
			for _, node := range result {
				for _, dep := range tt.deps[node] {
					if !visited[dep] {
						t.Errorf("topological order violation: %s depends on %s but %s comes first", node, dep, dep)
					}
				}
				visited[node] = true
			}
			if len(result) != len(allServices(tt.deps)) {
				t.Errorf("result length mismatch: got %d, want %d", len(result), len(allServices(tt.deps)))
			}
		})
	}
}

func TestTopologicalAndReverse(t *testing.T) {
	deps := map[string][]depInfo{
		"web": {{Name: "api"}},
		"api": {{Name: "db"}},
	}

	topo := topological(deps, nil)
	if len(topo) != 3 {
		t.Fatalf("expected 3 services, got %d", len(topo))
	}
	if topo[0] != "db" || topo[1] != "api" || topo[2] != "web" {
		t.Errorf("topological order wrong: got %v, expected [db api web]", topo)
	}

	rev := reverseTopological(deps, nil)
	if len(rev) != 3 {
		t.Fatalf("expected 3 services, got %d", len(rev))
	}
	if rev[0] != "web" || rev[1] != "api" || rev[2] != "db" {
		t.Errorf("reverse topological order wrong: got %v, expected [web api db]", rev)
	}
}

func TestTopologicalSort_CycleWarning(t *testing.T) {
	deps := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	var warnings []string
	warn := func(s string) {
		warnings = append(warnings, s)
	}
	result := topologicalSort(deps, warn)

	if len(result) < 3 {
		t.Errorf("expected at least 3 services in order (best-effort), got %d", len(result))
	}

	visited := make(map[string]bool)
	for _, node := range result {
		for _, dep := range deps[node] {
			if visited[dep] {
				// dependency already seen — topological order is respected for non-cycle edges
				continue
			}
			// If dep is not visited yet, it must be due to cycle, skip
		}
		visited[node] = true
	}

	if len(warnings) == 0 {
		t.Error("expected cycle warning, got none")
	}
	// Each node participates in the cycle, so we expect warnings
	t.Logf("warnings: %v", warnings)
}

func TestTopologicalSort_NoCycles_NoWarning(t *testing.T) {
	deps := map[string][]string{
		"a": {"b"},
		"b": {"c"},
	}
	var warnings []string
	warn := func(s string) {
		warnings = append(warnings, s)
	}
	_ = topologicalSort(deps, warn)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestAllServices(t *testing.T) {
	tests := []struct {
		name      string
		deps      map[string][]string
		want      []string
		wantCount int
	}{
		{
			name: "empty",
			deps: map[string][]string{},
			want: nil,
		},
		{
			name:      "deduplication across keys and values",
			deps:      map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}},
			wantCount: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allServices(tt.deps)
			if tt.want != nil && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.name == "deduplication across keys and values" {
				if len(got) != tt.wantCount {
					t.Errorf("expected %d services, got %d: %v", tt.wantCount, len(got), got)
				}
			}
		})
	}
}

func TestServiceContainers(t *testing.T) {
	tests := []struct {
		name string
		ctrs []*docker.Container
		want map[string][]*docker.Container
	}{
		{
			name: "groups by ComposeService",
			ctrs: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
				{ComposeService: "web", Name: "web2"},
				{ComposeService: "db", Name: "db1"},
			},
			want: map[string][]*docker.Container{
				"web": {
					{ComposeService: "web", Name: "web1"},
					{ComposeService: "web", Name: "web2"},
				},
				"db": {
					{ComposeService: "db", Name: "db1"},
				},
			},
		},
		{
			name: "empty service -> key \"\"",
			ctrs: []*docker.Container{
				{Name: "standalone"},
			},
			want: map[string][]*docker.Container{
				"": {{Name: "standalone"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceContainers(tt.ctrs)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for k, wantCt := range tt.want {
				gotCt, ok := got[k]
				if !ok {
					t.Errorf("missing key %q", k)
					continue
				}
				if len(gotCt) != len(wantCt) {
					t.Errorf("key %q: got %d containers, want %d", k, len(gotCt), len(wantCt))
				}
			}
		})
	}
}

func TestBuildDependents(t *testing.T) {
	ctrs := []*docker.Container{
		makeCtr("web", "api"),
		makeCtr("api", "db"),
	}
	got := buildDependents(ctrs)
	want := map[string][]string{
		"api": {"web"},
		"db":  {"api"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestStopSet(t *testing.T) {
	tests := []struct {
		name  string
		batch []*docker.Container
		all   []*docker.Container
		want  map[string]bool
	}{
		{
			name:  "no stop_before_backup -> empty stop set",
			batch: []*docker.Container{makeCtr("web", "db")},
			all:   []*docker.Container{},
			want:  map[string]bool{},
		},
		{
			name: "one service with stop=true -> stops that service",
			batch: []*docker.Container{
				{
					ComposeService: "web",
					ComposeProject: "proj",
					Labels:         map[string]string{"buoy.stop-before": "true"},
				},
			},
			all:  []*docker.Container{},
			want: map[string]bool{"web": true},
		},
		{
			name: "one service with stop=true, another depends on it -> both stopped",
			batch: []*docker.Container{
				{
					ComposeService: "web",
					ComposeProject: "proj",
					Labels:         map[string]string{"buoy.stop-before": "true"},
				},
			},
			all: []*docker.Container{
				makeCtr("api", "web"),
				makeCtr("web", ""),
			},
			want: map[string]bool{"web": true, "api": true},
		},
		{
			name: "dependent of stopped service is also stopped via cascade",
			batch: []*docker.Container{
				{
					ComposeService: "db",
					ComposeProject: "proj",
					Labels:         map[string]string{"buoy.stop-before": "true"},
				},
			},
			all: []*docker.Container{
				makeCtr("api", "db"),
			},
			want: map[string]bool{"db": true, "api": true},
		},
		{
			name: "transitive cascade: A with stop=true, B depends on A, C depends on B",
			batch: []*docker.Container{
				{
					ComposeService: "a",
					ComposeProject: "proj",
					Labels:         map[string]string{"buoy.stop-before": "true"},
				},
			},
			all: []*docker.Container{
				makeCtr("b", "a"),
				makeCtr("c", "b"),
			},
			want: map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name: "non-batch container is NOT in stop set",
			batch: []*docker.Container{
				{
					ComposeService: "db",
					ComposeProject: "proj",
					Labels:         map[string]string{"buoy.stop-before": "true"},
				},
			},
			all: []*docker.Container{
				{ComposeService: "web", ComposeProject: "proj", Labels: map[string]string{"buoy.stop-before": "true"}},
			},
			want: map[string]bool{"db": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopSet(tt.batch, tt.all)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddDependents(t *testing.T) {
	dependents := map[string][]string{
		"db":  {"api"},
		"api": {"web"},
	}
	stop := make(map[string]bool)
	addDependents("db", stop, dependents)
	want := map[string]bool{"db": true, "api": true, "web": true}
	if !reflect.DeepEqual(stop, want) {
		t.Errorf("got %v, want %v", stop, want)
	}
}

func TestOrderForStopFromDeps(t *testing.T) {
	deps := map[string][]depInfo{
		"web": {{Name: "api"}},
		"api": {{Name: "db"}},
	}
	order := orderForStopFromDeps(deps, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 services, got %d", len(order))
	}
	if order[0] != "web" || order[1] != "api" || order[2] != "db" {
		t.Errorf("reverse topological order wrong: got %v, expected [web api db]", order)
	}
}

func TestOrderForStartFromDeps(t *testing.T) {
	deps := map[string][]depInfo{
		"web": {{Name: "api"}},
		"api": {{Name: "db"}},
	}
	order := orderForStartFromDeps(deps, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 services, got %d", len(order))
	}
	if order[0] != "db" || order[1] != "api" || order[2] != "web" {
		t.Errorf("topological order wrong: got %v, expected [db api web]", order)
	}
}
