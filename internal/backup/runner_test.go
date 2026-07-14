package backup

import (
	"testing"

	"github.com/depado/buoy/internal/docker"
)

func TestMapKeys(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]bool
	}{
		{"empty map", map[string]bool{}},
		{"map with entries", map[string]bool{"a": true, "b": false, "c": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapKeys(tt.m)
			if len(got) != len(tt.m) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.m))
			}
			seen := make(map[string]bool)
			for _, k := range got {
				if _, ok := tt.m[k]; !ok {
					t.Errorf("key %q not in original map", k)
				}
				seen[k] = true
			}
			for k := range tt.m {
				if !seen[k] {
					t.Errorf("key %q missing from result", k)
				}
			}
		})
	}
}

func TestDeduplicateByService(t *testing.T) {
	tests := []struct {
		name     string
		input    []*docker.Container
		wantCnt  int
		wantSvcs []string // expected service names in order
	}{
		{
			name:    "empty",
			input:   nil,
			wantCnt: 0,
		},
		{
			name: "single",
			input: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "duplicates by service name -> one kept",
			input: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
				{ComposeService: "web", Name: "web2"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "standalone containers (no service) -> never deduplicated",
			input: []*docker.Container{
				{Name: "standalone1"},
				{Name: "standalone2"},
			},
			wantCnt:  2,
			wantSvcs: []string{"", ""},
		},
		{
			name: "mix of duped services and standalones",
			input: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
				{Name: "standalone1"},
				{ComposeService: "web", Name: "web2"},
				{Name: "standalone2"},
				{ComposeService: "db", Name: "db1"},
			},
			wantCnt:  4,
			wantSvcs: []string{"web", "", "", "db"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateByService(tt.input)
			if len(got) != tt.wantCnt {
				t.Errorf("count: got %d, want %d", len(got), tt.wantCnt)
			}
			if len(got) != len(tt.wantSvcs) {
				t.Fatalf("wantSvcs length mismatch: got %d, want %d", len(got), len(tt.wantSvcs))
			}
			for i, c := range got {
				if c.ComposeService != tt.wantSvcs[i] {
					t.Errorf("index %d: got service %q, want %q", i, c.ComposeService, tt.wantSvcs[i])
				}
			}
		})
	}
}
