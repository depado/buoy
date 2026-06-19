package notify

import (
	"log/slog"
	"os"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Level
	}{
		{"error", "error", LevelError},
		{"all", "all", LevelAll},
		{"none", "none", LevelNone},
		{"empty", "", LevelNone},
		{"unknown", "debug", LevelNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNew_Disabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	n, err := New(nil, LevelNone, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != nil {
		t.Error("expected nil notifier when level is none")
	}

	n, err = New([]string{}, LevelError, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != nil {
		t.Error("expected nil notifier when urls is empty")
	}
}

func TestSendBackupError_NilNotifier(t *testing.T) {
	var n *Notifier
	// Must not panic.
	n.SendBackupError("test-container", "test error message")
}

func TestSendInfo_NilNotifier(t *testing.T) {
	var n *Notifier
	// Must not panic.
	n.SendInfo("title", "test message")
}
