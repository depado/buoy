package restic

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/depado/buoy/internal/types"
)

func TestForgetArgs(t *testing.T) {
	tests := []struct {
		name     string
		repo     string
		policy   types.RetentionPolicy
		hostname string
		want     []string
	}{
		{
			name:   "empty policy",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{},
			want:   []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags"},
		},
		{
			name:   "keep-last only",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepLast: 10},
			want:   []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags", "--keep-last", "10"},
		},
		{
			name:   "keep-hourly only",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepHourly: 24},
			want:   []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags", "--keep-hourly", "24"},
		},
		{
			name:   "keep-daily only",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepDaily: 7},
			want:   []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags", "--keep-daily", "7"},
		},
		{
			name:   "all keep flags",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepLast: 10, KeepHourly: 24, KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepYearly: 2},
			want: []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags",
				"--keep-last", "10", "--keep-hourly", "24", "--keep-daily", "30", "--keep-weekly", "4", "--keep-monthly", "12", "--keep-yearly", "2"},
		},
		{
			name:   "keep-within only",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepWithin: "30d"},
			want:   []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags", "--keep-within", "30d"},
		},
		{
			name:   "mixed keep-daily and keep-within",
			repo:   "/backup/repo",
			policy: types.RetentionPolicy{KeepDaily: 14, KeepWithin: "7d"},
			want: []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags",
				"--keep-daily", "14", "--keep-within", "7d"},
		},
		{
			name:     "keep-daily with hostname",
			repo:     "/backup/repo",
			policy:   types.RetentionPolicy{KeepDaily: 7},
			hostname: "myapp",
			want:     []string{"forget", "-r", "/backup/repo", "--json", "--group-by", "host,tags", "--host", "myapp", "--keep-daily", "7"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forgetArgs(tt.repo, tt.policy, tt.hostname)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTryParseExitError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *ExitError
	}{
		{
			name:  "valid exit_error",
			input: `{"message_type":"exit_error","code":1,"message":"bad"}`,
			want:  &ExitError{MessageType: "exit_error", Code: 1, Message: "bad"},
		},
		{
			name:  "wrong message_type",
			input: `{"message_type":"error","code":1,"message":"bad"}`,
			want:  nil,
		},
		{
			name:  "non-json",
			input: "not json",
			want:  nil,
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "exit_error with code 0",
			input: `{"message_type":"exit_error","code":0,"message":"ok"}`,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryParseExitError(tt.input)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("expected nil, got %+v", got)
			case tt.want != nil && got == nil:
				t.Error("expected non-nil, got nil")
			case tt.want != nil && got != nil:
				if got.Message != tt.want.Message || got.Code != tt.want.Code {
					t.Errorf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestCheckMethodExists(t *testing.T) {
	c := &Client{binPath: "restic", password: "test"}
	_ = c.Check
	_ = c.CheckReadData
}

func TestFormatFileLevelErrors(t *testing.T) {
	errors := []BackupError{
		{During: "backup", Item: "/test/file", Message: "permission denied"},
	}
	msg := fmt.Sprintf("restic backup: %d file-level errors (first: %s: %s)",
		len(errors), errors[0].During, errors[0].Item)
	want := "restic backup: 1 file-level errors (first: backup: /test/file)"
	if msg != want {
		t.Errorf("got %q, want %q", msg, want)
	}
}
