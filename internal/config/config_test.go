package config

import (
	"os"
	"strings"
	"testing"
)

func TestParseRepoSuffix(t *testing.T) {
	tests := []struct {
		suffix    string
		wantName  string
		wantField string
		wantOK    bool
	}{
		{"MYREPO_URL", "myrepo", "url", true},
		{"MYREPO_PASSWORD", "myrepo", "password", true},
		{"myrepo_url", "myrepo", "url", true},
		{"myrepo_password", "myrepo", "password", true},
		{"REPO_NAME_URL", "repo_name", "url", true},
		{"REPO_NAME_PASSWORD", "repo_name", "password", true},
		{"A1_B2_C3_URL", "a1_b2_c3", "url", true},
		{"MYREPO_OTHER", "", "", false},
		{"MYREPO", "", "", false},
		{"_URL", "", "", false},
		{"MYREPO_", "", "", false},
		{"", "", "", false},
		{"123invalid_URL", "123invalid", "url", true},
		{"_invalid_URL", "", "", false},
		{"-invalid_URL", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.suffix, func(t *testing.T) {
			name, field, ok := parseRepoSuffix(tt.suffix)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if name != tt.wantName {
				t.Errorf("name: got %q, want %q", name, tt.wantName)
			}
			if field != tt.wantField {
				t.Errorf("field: got %q, want %q", field, tt.wantField)
			}
		})
	}
}

func TestScanEnvRepos(t *testing.T) {
	t.Run("single repo with URL and password", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/backup")
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_PASSWORD", "local-pass")

		rc := &ResticConf{Password: "global-pass"}
		ScanEnvRepos(rc)

		if len(rc.Repos) != 1 {
			t.Fatalf("expected 1 repo, got %d", len(rc.Repos))
		}
		c, ok := rc.Repos["local"]
		if !ok {
			t.Fatal("repo 'local' not found")
		}
		if c.URL != "/backup" {
			t.Errorf("URL: got %q, want /backup", c.URL)
		}
		if c.Password != "local-pass" {
			t.Errorf("Password: got %q, want local-pass", c.Password)
		}
	})

	t.Run("multiple repos", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/backup")
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_PASSWORD", "lp")
		t.Setenv("BUOY_RESTIC_REPO_S3_URL", "s3:https://bucket")
		t.Setenv("BUOY_RESTIC_REPO_S3_PASSWORD", "sp")

		rc := &ResticConf{}
		ScanEnvRepos(rc)

		if len(rc.Repos) != 2 {
			t.Fatalf("expected 2 repos, got %d", len(rc.Repos))
		}
	})

	t.Run("config file takes precedence", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/env-backup")
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_PASSWORD", "env-pass")

		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"local": {URL: "/cfg-backup", Password: "cfg-pass"},
			},
		}
		ScanEnvRepos(rc)

		c := rc.Repos["local"]
		if c.URL != "/cfg-backup" {
			t.Errorf("URL: config file should win, got %q", c.URL)
		}
		if c.Password != "cfg-pass" {
			t.Errorf("Password: config file should win, got %q", c.Password)
		}
	})

	t.Run("repo without password uses global", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/backup")

		rc := &ResticConf{Password: "global-pass"}
		ScanEnvRepos(rc)

		c := rc.Repos["local"]
		if c.Password != "" {
			t.Errorf("Password: env-only URL should have empty password, got %q", c.Password)
		}
	})

	t.Run("no matching env vars", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{},
		}
		ScanEnvRepos(rc)
		if len(rc.Repos) != 0 {
			t.Errorf("expected 0 repos, got %d", len(rc.Repos))
		}
	})

	t.Run("nil Repos map", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/backup")
		rc := &ResticConf{}
		ScanEnvRepos(rc)
		if rc.Repos == nil {
			t.Fatal("Repos should be initialized")
		}
	})

	t.Run("invalid repo names are skipped", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO__INVALID_URL", "/backup")

		rc := &ResticConf{}
		ScanEnvRepos(rc)

		if len(rc.Repos) != 0 {
			t.Errorf("expected 0 repos, got %d", len(rc.Repos))
		}
	})

	t.Run("URL-only env adds repo with empty password", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_URL", "/backup")

		rc := &ResticConf{}
		ScanEnvRepos(rc)

		c, ok := rc.Repos["local"]
		if !ok {
			t.Fatal("repo 'local' not found")
		}
		if c.URL != "/backup" {
			t.Errorf("URL: got %q", c.URL)
		}
		if c.Password != "" {
			t.Errorf("Password: got %q, want empty", c.Password)
		}
	})

	t.Run("password-only env adds repo with empty URL", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_LOCAL_PASSWORD", "secret")

		rc := &ResticConf{}
		ScanEnvRepos(rc)

		c, ok := rc.Repos["local"]
		if !ok {
			t.Fatal("repo 'local' not found")
		}
		if c.URL != "" {
			t.Errorf("URL: got %q, want empty", c.URL)
		}
	})

	t.Run("mixed case names become lowercase", func(t *testing.T) {
		t.Setenv("BUOY_RESTIC_REPO_MyRepo_URL", "/backup")

		rc := &ResticConf{}
		ScanEnvRepos(rc)

		if _, ok := rc.Repos["myrepo"]; !ok {
			t.Fatal("repo 'myrepo' not found (case normalization failed)")
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid with global password", func(t *testing.T) {
		rc := &ResticConf{
			Password: "global",
			Repos: map[string]RepoConfig{
				"local": {URL: "/backup"},
				"s3":    {URL: "s3:https://bucket", Password: "s3-pass"},
			},
		}
		if err := rc.Validate(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("valid with all per-repo passwords", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"local": {URL: "/backup", Password: "lp"},
				"s3":    {URL: "s3:https://bucket", Password: "sp"},
			},
		}
		if err := rc.Validate(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("no repos", func(t *testing.T) {
		rc := &ResticConf{}
		err := rc.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "required") {
			t.Errorf("expected 'required' in error, got: %v", err)
		}
	})

	t.Run("invalid repo name", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"_bad": {URL: "/backup", Password: "pw"},
			},
		}
		err := rc.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid") {
			t.Errorf("expected 'invalid' in error, got: %v", err)
		}
	})

	t.Run("empty URL", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"local": {Password: "pw"},
			},
		}
		err := rc.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "empty URL") {
			t.Errorf("expected 'empty URL' in error, got: %v", err)
		}
	})

	t.Run("duplicate URL", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"a": {URL: "/backup", Password: "pw"},
				"b": {URL: "/backup", Password: "pw"},
			},
		}
		err := rc.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("expected 'duplicate' in error, got: %v", err)
		}
	})

	t.Run("missing password with no global", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"local": {URL: "/backup"},
			},
		}
		err := rc.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no password") {
			t.Errorf("expected 'no password' in error, got: %v", err)
		}
	})

	t.Run("invalid repo name with underscore prefix", func(t *testing.T) {
		rc := &ResticConf{
			Repos: map[string]RepoConfig{
				"_bad": {URL: "/backup", Password: "pw"},
			},
		}
		if err := rc.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestPasswordFor(t *testing.T) {
	rc := &ResticConf{
		Password: "global",
		Repos: map[string]RepoConfig{
			"local": {URL: "/backup"},
			"s3":    {URL: "s3:https://bucket", Password: "s3-pass"},
		},
	}

	t.Run("explicit per-repo password", func(t *testing.T) {
		if got := rc.PasswordFor("s3"); got != "s3-pass" {
			t.Errorf("got %q, want s3-pass", got)
		}
	})

	t.Run("falls back to global", func(t *testing.T) {
		if got := rc.PasswordFor("local"); got != "global" {
			t.Errorf("got %q, want global", got)
		}
	})

	t.Run("unknown repo name falls back to global", func(t *testing.T) {
		if got := rc.PasswordFor("unknown"); got != "global" {
			t.Errorf("got %q, want global", got)
		}
	})

	t.Run("empty global falls back to empty", func(t *testing.T) {
		empty := &ResticConf{
			Repos: map[string]RepoConfig{
				"local": {URL: "/backup"},
			},
		}
		if got := empty.PasswordFor("local"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestToRepoRefs(t *testing.T) {
	repos := map[string]RepoConfig{
		"b": {URL: "/b"},
		"a": {URL: "/a"},
		"c": {URL: "/c"},
	}

	refs, list := ToRepoRefs(repos)

	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d", len(refs))
	}
	if refs[0].Name != "a" {
		t.Errorf("first should be 'a', got %q", refs[0].Name)
	}
	if refs[1].Name != "b" {
		t.Errorf("second should be 'b', got %q", refs[1].Name)
	}
	if refs[2].Name != "c" {
		t.Errorf("third should be 'c', got %q", refs[2].Name)
	}

	if len(list) != 3 {
		t.Fatalf("expected 3 log entries, got %d", len(list))
	}
	if list[0] != "a:/a" {
		t.Errorf("first log entry: got %q", list[0])
	}
}

func TestScanEnvReposCleanEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BUOY_RESTIC_REPO_") {
			t.Fatalf("BUOY_RESTIC_REPO_ env vars leaked into test: %q", kv)
		}
	}
}
