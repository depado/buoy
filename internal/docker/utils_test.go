package docker

import (
	"testing"
	"time"
)

func TestGetString(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		key      string
		fallback string
		want     string
	}{
		{"key present", map[string]string{"foo": "bar"}, "foo", "default", "bar"},
		{"key missing", map[string]string{}, "foo", "default", "default"},
		{"nil labels", nil, "foo", "default", "default"},
		{"empty value", map[string]string{"foo": ""}, "foo", "default", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getString(tt.labels, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetSlice(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		key    string
		want   []string
	}{
		{"key present", map[string]string{"foo": "a, b, c"}, "foo", []string{"a", "b", "c"}},
		{"key missing", map[string]string{}, "foo", nil},
		{"nil labels", nil, "foo", nil},
		{"single value", map[string]string{"foo": "only"}, "foo", []string{"only"}},
		{"empty value", map[string]string{"foo": ""}, "foo", nil},
		{"whitespace trimming", map[string]string{"foo": " a ,  b "}, "foo", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSlice(tt.labels, tt.key)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGetBool(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		key      string
		fallback bool
		want     bool
	}{
		{"true", map[string]string{"foo": "true"}, "foo", false, true},
		{"false", map[string]string{"foo": "false"}, "foo", true, false},
		{"key missing", map[string]string{}, "foo", true, true},
		{"nil labels", nil, "foo", true, true},
		{"invalid value", map[string]string{"foo": "bad"}, "foo", true, true},
		{"invalid value fallback false", map[string]string{"foo": "bad"}, "foo", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBool(tt.labels, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDuration(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		key      string
		fallback time.Duration
		want     time.Duration
	}{
		{"valid duration", map[string]string{"foo": "5m"}, "foo", time.Second, 5 * time.Minute},
		{"key missing", map[string]string{}, "foo", time.Second, time.Second},
		{"nil labels", nil, "foo", time.Minute, time.Minute},
		{"invalid value", map[string]string{"foo": "bad"}, "foo", 30 * time.Second, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDuration(tt.labels, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetNonZero(t *testing.T) {
	t.Run("int non-zero replaces", func(t *testing.T) {
		dst := 0
		setNonZero(&dst, 42)
		if dst != 42 {
			t.Errorf("got %d, want 42", dst)
		}
	})

	t.Run("int zero does not replace", func(t *testing.T) {
		dst := 10
		setNonZero(&dst, 0)
		if dst != 10 {
			t.Errorf("got %d, want 10", dst)
		}
	})

	t.Run("string non-zero replaces", func(t *testing.T) {
		dst := ""
		setNonZero(&dst, "hello")
		if dst != "hello" {
			t.Errorf("got %q, want %q", dst, "hello")
		}
	})

	t.Run("string zero does not replace", func(t *testing.T) {
		dst := "keep"
		setNonZero(&dst, "")
		if dst != "keep" {
			t.Errorf("got %q, want %q", dst, "keep")
		}
	})
}
