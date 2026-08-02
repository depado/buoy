package scheduler

import (
	"io"
	"log/slog"
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/types"
)

func newTestCron() *cron.Cron {
	return cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func makeContainer(id, project, service string, labels map[string]string) *types.Container {
	return &types.Container{
		ID:             id,
		Name:           id,
		ComposeProject: project,
		ComposeService: service,
		Labels:         labels,
	}
}

func makeNamedContainer(id, name, project, service string) *types.Container {
	return &types.Container{
		ID:             id,
		Name:           name,
		ComposeProject: project,
		ComposeService: service,
	}
}

func TestContainerRegistry_RegisterAndHas(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	ctr := makeContainer("abc", "proj", "web", map[string]string{"buoy.schedule": "@daily"})

	if cr.has("abc") {
		t.Error("expected not found before registration")
	}

	if err := cr.register(ctr, "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if !cr.has("abc") {
		t.Error("expected found after registration")
	}
}

func TestContainerRegistry_DuplicateRegister(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	ctr := makeContainer("abc", "proj", "web", nil)

	if err := cr.register(ctr, "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := cr.register(ctr, "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("second register: %v", err)
	}

	if got := cr.getGroup("proj::@daily"); len(got) != 1 {
		t.Errorf("expected 1 container in group, got %d", len(got))
	}
}

func TestContainerRegistry_Unregister(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	ctr := makeContainer("abc", "proj", "web", nil)

	if err := cr.register(ctr, "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}

	cr.unregister("abc")

	if cr.has("abc") {
		t.Error("expected not found after unregister")
	}
	if got := cr.getGroup("proj::@daily"); len(got) != 0 {
		t.Errorf("expected empty group, got %d", len(got))
	}
}

func TestContainerRegistry_MultipleContainersSameGroup(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())

	if err := cr.register(makeContainer("a", "proj", "web", nil), "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := cr.register(makeContainer("b", "proj", "api", nil), "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	group := cr.getGroup("proj::@daily")
	if len(group) != 2 {
		t.Errorf("expected 2 containers, got %d", len(group))
	}
}

func TestContainerRegistry_ForEachEntry(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())

	if err := cr.register(makeContainer("a", "proj", "web", nil), "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := cr.register(makeContainer("b", "", "standalone", nil), "::@every 6h", "@every 6h", func() {}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	count := 0
	cr.forEachEntry(func(id, key string) bool {
		count++
		return true
	})
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}
}

func TestScheduleGroupKey(t *testing.T) {
	tests := []struct {
		name     string
		project  string
		schedule string
		want     string
	}{
		{
			name:     "project + schedule",
			project:  "myproject",
			schedule: "@daily",
			want:     "myproject::@daily",
		},
		{
			name:     "empty project + schedule",
			project:  "",
			schedule: "@every 6h",
			want:     "::@every 6h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scheduleGroupKey(tt.project, tt.schedule)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContainerRegistry_FindByName(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	ctr := makeNamedContainer("abc123", "uptime-kuma", "uptime-kuma", "uptime-kuma")
	if err := cr.register(ctr, "uptime-kuma::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}

	got := cr.find("uptime-kuma")
	if got == nil {
		t.Fatal("expected found by name")
	}
	if got.ID != "abc123" {
		t.Errorf("got ID %q, want %q", got.ID, "abc123")
	}
}

func TestContainerRegistry_FindNotFound(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	ctr := makeNamedContainer("abc123", "myapp", "proj", "web")
	if err := cr.register(ctr, "proj::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if got := cr.find("nonexistent"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestContainerRegistry_FindByProject(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())

	if err := cr.register(makeNamedContainer("a", "db", "myapp", "db"), "myapp::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := cr.register(makeNamedContainer("b", "api", "myapp", "api"), "myapp::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := cr.register(makeNamedContainer("c", "web", "myapp", "web"), "myapp::@daily", "@daily", func() {}); err != nil {
		t.Fatalf("register c: %v", err)
	}

	result := cr.findByProject("myapp")
	if len(result) != 3 {
		t.Fatalf("expected 3 containers, got %d", len(result))
	}
}

func TestContainerRegistry_FindByProject_Empty(t *testing.T) {
	cr := newContainerRegistry(newTestCron(), newTestLogger())
	result := cr.findByProject("nonexistent")
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}
