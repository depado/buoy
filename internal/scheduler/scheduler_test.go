package scheduler

import (
	"io"
	"log/slog"
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/docker"
)

func newTestCron() *cron.Cron {
	return cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func makeContainer(id, project, service string, labels map[string]string) *docker.Container {
	return &docker.Container{
		ID:             id,
		ComposeProject: project,
		ComposeService: service,
		Labels:         labels,
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
