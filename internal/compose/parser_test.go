package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseShortSyntax(t *testing.T) {
	tests := []struct {
		name    string
		svcName string
		spec    string
		baseDir string
		want    VolumeEntry
		ok      bool
	}{
		{
			name:    "absolute bind mount",
			svcName: "web",
			spec:    "/host/data:/container/data",
			baseDir: "/app",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "/host/data", Resolved: "/host/data", Destination: "/container/data", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "relative bind mount",
			svcName: "web",
			spec:    "./data:/container/data",
			baseDir: "/app",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "./data", Resolved: "/app/data", Destination: "/container/data", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "relative parent dir bind mount",
			svcName: "web",
			spec:    "../shared:/container/shared",
			baseDir: "/app/web",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "../shared", Resolved: "/app/shared", Destination: "/container/shared", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "bind mount read-only",
			svcName: "api",
			spec:    "/etc/config:/etc/config:ro",
			baseDir: "/app",
			want:    VolumeEntry{Service: "api", Type: "bind", Source: "/etc/config", Resolved: "/etc/config", Destination: "/etc/config", Mode: "ro"},
			ok:      true,
		},
		{
			name:    "bind mount read-write explicit",
			svcName: "db",
			spec:    "/data/pg:/var/lib/postgresql:rw",
			baseDir: "/app",
			want:    VolumeEntry{Service: "db", Type: "bind", Source: "/data/pg", Resolved: "/data/pg", Destination: "/var/lib/postgresql", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "named volume",
			svcName: "db",
			spec:    "db_data:/var/lib/mysql",
			baseDir: "/app",
			want:    VolumeEntry{Service: "db", Type: "volume", Source: "db_data", Destination: "/var/lib/mysql", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "named volume read-only",
			svcName: "app",
			spec:    "app_data:/data:ro",
			baseDir: "/app",
			want:    VolumeEntry{Service: "app", Type: "volume", Source: "app_data", Destination: "/data", Mode: "ro"},
			ok:      true,
		},
		{
			name:    "invalid short syntax (no target)",
			svcName: "bad",
			spec:    "only_source",
			baseDir: "/app",
			want:    VolumeEntry{},
			ok:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseShortSyntax(tt.svcName, tt.spec, tt.baseDir, nil)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Service != tt.want.Service {
				t.Errorf("Service = %q, want %q", got.Service, tt.want.Service)
			}
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.Source != tt.want.Source {
				t.Errorf("Source = %q, want %q", got.Source, tt.want.Source)
			}
			if got.Destination != tt.want.Destination {
				t.Errorf("Destination = %q, want %q", got.Destination, tt.want.Destination)
			}
			if got.Mode != tt.want.Mode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.want.Mode)
			}
			if got.Resolved != tt.want.Resolved {
				t.Errorf("Resolved = %q, want %q", got.Resolved, tt.want.Resolved)
			}
		})
	}
}

func TestParseLongSyntax(t *testing.T) {
	tests := []struct {
		name    string
		svcName string
		m       map[string]any
		baseDir string
		want    VolumeEntry
		ok      bool
	}{
		{
			name:    "long syntax bind mount",
			svcName: "web",
			m: map[string]any{
				"type":   "bind",
				"source": "/host/data",
				"target": "/container/data",
			},
			baseDir: "/app",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "/host/data", Resolved: "/host/data", Destination: "/container/data", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "long syntax bind mount read-only",
			svcName: "web",
			m: map[string]any{
				"type":      "bind",
				"source":    "/host/config",
				"target":    "/container/config",
				"read_only": true,
			},
			baseDir: "/app",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "/host/config", Resolved: "/host/config", Destination: "/container/config", Mode: "ro"},
			ok:      true,
		},
		{
			name:    "long syntax named volume",
			svcName: "db",
			m: map[string]any{
				"type":   "volume",
				"source": "db_data",
				"target": "/var/lib/mysql",
			},
			baseDir: "/app",
			want:    VolumeEntry{Service: "db", Type: "volume", Source: "db_data", Destination: "/var/lib/mysql", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "long syntax relative bind mount",
			svcName: "web",
			m: map[string]any{
				"type":   "bind",
				"source": "./html",
				"target": "/usr/share/nginx/html",
			},
			baseDir: "/app/web",
			want:    VolumeEntry{Service: "web", Type: "bind", Source: "./html", Resolved: "/app/web/html", Destination: "/usr/share/nginx/html", Mode: "rw"},
			ok:      true,
		},
		{
			name:    "long syntax missing type",
			svcName: "bad",
			m: map[string]any{
				"source": "/data",
				"target": "/data",
			},
			baseDir: "/app",
			want:    VolumeEntry{},
			ok:      false,
		},
		{
			name:    "long syntax missing source",
			svcName: "bad",
			m: map[string]any{
				"type":   "bind",
				"target": "/data",
			},
			baseDir: "/app",
			want:    VolumeEntry{},
			ok:      false,
		},
		{
			name:    "long syntax missing target",
			svcName: "bad",
			m: map[string]any{
				"type":   "bind",
				"source": "/data",
			},
			baseDir: "/app",
			want:    VolumeEntry{},
			ok:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLongSyntax(tt.svcName, tt.m, tt.baseDir, nil)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Service != tt.want.Service {
				t.Errorf("Service = %q, want %q", got.Service, tt.want.Service)
			}
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.Source != tt.want.Source {
				t.Errorf("Source = %q, want %q", got.Source, tt.want.Source)
			}
			if got.Destination != tt.want.Destination {
				t.Errorf("Destination = %q, want %q", got.Destination, tt.want.Destination)
			}
			if got.Mode != tt.want.Mode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.want.Mode)
			}
			if got.Resolved != tt.want.Resolved {
				t.Errorf("Resolved = %q, want %q", got.Resolved, tt.want.Resolved)
			}
		})
	}
}

func TestParseCompose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")

	data := []byte(`
services:
  web:
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./html:/usr/share/nginx/html
      - type: bind
        source: /etc/nginx/conf.d
        target: /etc/nginx/conf.d
        read_only: true

  db:
    volumes:
      - db_data:/var/lib/postgresql/data

  api:
    volumes:
      - /home/user/uploads:/app/uploads:rw
      - cache_data:/app/cache

volumes:
  db_data:
  cache_data:
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(stacks) != 1 {
		t.Fatalf("got %d stacks, want 1", len(stacks))
	}

	stack := stacks[0]
	if stack.Path != path {
		t.Errorf("path = %q, want %q", stack.Path, path)
	}
	if len(stack.Services) != 3 {
		t.Fatalf("got %d services, want 3", len(stack.Services))
	}

	totalVolumes := 0
	for _, s := range stack.Services {
		totalVolumes += len(s.Volumes)
	}
	if totalVolumes != 6 {
		t.Fatalf("got %d total volumes, want 6", totalVolumes)
	}

	type check struct {
		service     string
		typ         string
		source      string
		destination string
		mode        string
	}

	want := map[string]check{
		"web:/var/run/docker.sock": {"web", "bind", "/var/run/docker.sock", "/var/run/docker.sock", "ro"},
		"web:./html":               {"web", "bind", "./html", "/usr/share/nginx/html", "rw"},
		"web:/etc/nginx/conf.d":    {"web", "bind", "/etc/nginx/conf.d", "/etc/nginx/conf.d", "ro"},
		"db:db_data":               {"db", "volume", "db_data", "/var/lib/postgresql/data", "rw"},
		"api:/home/user/uploads":   {"api", "bind", "/home/user/uploads", "/app/uploads", "rw"},
		"api:cache_data":           {"api", "volume", "cache_data", "/app/cache", "rw"},
	}

	for _, s := range stack.Services {
		for _, e := range s.Volumes {
			key := e.Service + ":" + e.Source
			w, ok := want[key]
			if !ok {
				t.Errorf("unexpected entry: %s (service=%s, source=%s)", key, e.Service, e.Source)
				continue
			}
			if e.Service != w.service {
				t.Errorf("%s: service = %q, want %q", key, e.Service, w.service)
			}
			if e.Type != w.typ {
				t.Errorf("%s: type = %q, want %q", key, e.Type, w.typ)
			}
			if e.Source != w.source {
				t.Errorf("%s: source = %q, want %q", key, e.Source, w.source)
			}
			if e.Destination != w.destination {
				t.Errorf("%s: destination = %q, want %q", key, e.Destination, w.destination)
			}
			if e.Mode != w.mode {
				t.Errorf("%s: mode = %q, want %q", key, e.Mode, w.mode)
			}
		}
	}
}

func TestParseComposeLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")

	data := []byte(`
services:
  db:
    labels:
      buoy.enabled: "true"
      buoy.schedule: "@daily"
      buoy.exclude: "/tmp/scratch"
    volumes:
      - db_data:/var/lib/mysql
      - /tmp/scratch:/scratch
      - /data/backups:/backups

  web:
    labels:
      buoy.enabled: "false"
    volumes:
      - ./html:/usr/share/nginx/html

volumes:
  db_data:
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(stacks) != 1 {
		t.Fatalf("got %d stacks, want 1", len(stacks))
	}

	stack := stacks[0]
	if len(stack.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(stack.Services))
	}

	serviceMap := make(map[string]ServiceInfo)
	for _, s := range stack.Services {
		serviceMap[s.Name] = s
	}

	db := serviceMap["db"]
	web := serviceMap["web"]

	if db.Labels["buoy.enabled"] != "true" {
		t.Errorf("db enabled label: got %q, want \"true\"", db.Labels["buoy.enabled"])
	}
	if db.Labels["buoy.schedule"] != "@daily" {
		t.Errorf("db schedule label: got %q, want \"@daily\"", db.Labels["buoy.schedule"])
	}
	if db.Labels["buoy.exclude"] != "/tmp/scratch" {
		t.Errorf("db exclude label: got %q, want \"/tmp/scratch\"", db.Labels["buoy.exclude"])
	}
	if len(db.Volumes) != 3 {
		t.Errorf("db volumes: got %d, want 3", len(db.Volumes))
	}

	if web.Labels["buoy.enabled"] != "false" {
		t.Errorf("web enabled label: got %q, want \"false\"", web.Labels["buoy.enabled"])
	}
	if len(web.Volumes) != 1 {
		t.Errorf("web volumes: got %d, want 1", len(web.Volumes))
	}
}

func TestParseComposeNoVolumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")

	data := []byte(`
services:
  web:
    image: nginx:latest
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, false)
	if err != nil {
		t.Fatal(err)
	}

	totalVolumes := 0
	for _, s := range stacks[0].Services {
		totalVolumes += len(s.Volumes)
	}
	if totalVolumes != 0 {
		t.Errorf("expected 0 volumes, got %d", totalVolumes)
	}
}

func TestDiscoverNoComposeFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Discover(dir, 0, DefaultPatterns, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDiscoverNotADirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(path, 0, DefaultPatterns, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseComposeDockerComposeFileName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	data := []byte(`
services:
  app:
    volumes:
      - app_data:/data
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(stacks) != 1 || len(stacks[0].Services) != 1 {
		t.Fatalf("got %d stacks, %d services", len(stacks), len(stacks[0].Services))
	}

	e := stacks[0].Services[0].Volumes[0]
	if e.Service != "app" || e.Type != "volume" || e.Source != "app_data" {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestDiscoverRecursive(t *testing.T) {
	root := t.TempDir()

	writeCompose := func(subdir, content string) {
		dir := filepath.Join(root, subdir)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	writeCompose(".", `
services:
  gateway:
    volumes:
      - /etc/nginx:/etc/nginx:ro
`)

	writeCompose("project-a", `
services:
  api:
    volumes:
      - api_data:/data
`)

	writeCompose("project-b", `
services:
  db:
    volumes:
      - ./backups:/backups
`)

	writeCompose("nested/deep", `
services:
  cache:
    volumes:
      - cache_data:/data
`)

	t.Run("depth 0 — only root", func(t *testing.T) {
		stacks, err := Discover(root, 0, DefaultPatterns, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(stacks) != 1 {
			t.Fatalf("depth 0: got %d stacks, want 1", len(stacks))
		}
		if stacks[0].Services[0].Name != "gateway" {
			t.Errorf("expected gateway, got %s", stacks[0].Services[0].Name)
		}
	})

	t.Run("depth 1 — root + immediate children", func(t *testing.T) {
		stacks, err := Discover(root, 1, DefaultPatterns, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(stacks) != 3 {
			t.Fatalf("depth 1: got %d stacks, want 3", len(stacks))
		}
		names := make(map[string]bool)
		for _, s := range stacks {
			for _, svc := range s.Services {
				names[svc.Name] = true
			}
		}
		if !names["gateway"] || !names["api"] || !names["db"] {
			t.Errorf("missing services, got: %v", names)
		}
	})

	t.Run("depth 2 — includes nested/deep", func(t *testing.T) {
		stacks, err := Discover(root, 2, DefaultPatterns, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(stacks) != 4 {
			t.Fatalf("depth 2: got %d stacks, want 4", len(stacks))
		}
	})

	t.Run("depth -1 — unlimited", func(t *testing.T) {
		stacks, err := Discover(root, -1, DefaultPatterns, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(stacks) != 4 {
			t.Fatalf("depth -1: got %d stacks, want 4", len(stacks))
		}
	})
}

func TestDiscoverRecursiveEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested", "deep"), 0750); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(root, 1, DefaultPatterns, false)
	if err == nil {
		t.Fatal("expected error for directory with no compose files")
	}
}

func TestDiscoverCustomPatterns(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(`
services:
  app:
    volumes:
      - data:/data
`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "stack.prod.yml"), []byte(`
services:
  worker:
    volumes:
      - jobs:/jobs
`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "not-a-compose.txt"), []byte(`not yaml`), 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(root, 0, []string{"stack.*.yml"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 1 {
		t.Fatalf("got %d stacks, want 1", len(stacks))
	}
	if stacks[0].Services[0].Name != "worker" {
		t.Errorf("got service %q, want worker", stacks[0].Services[0].Name)
	}
}

func TestParseComposeListLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")

	data := []byte(`
services:
  db:
    labels:
      - "buoy.enabled=true"
      - "buoy.schedule=@daily"
      - "traefik.enable=true"
    volumes:
      - db_data:/var/lib/mysql

  web:
    labels:
      buoy.enabled: "false"
      traefik.port: "80"
    volumes:
      - ./html:/usr/share/nginx/html

volumes:
  db_data:
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(stacks) != 1 || len(stacks[0].Services) != 2 {
		t.Fatalf("got %d stacks, %d services", len(stacks), len(stacks[0].Services))
	}

	for _, s := range stacks[0].Services {
		switch s.Name {
		case "db":
			if s.Labels["buoy.enabled"] != "true" {
				t.Errorf("db enabled: got %q", s.Labels["buoy.enabled"])
			}
			if s.Labels["traefik.enable"] != "true" {
				t.Errorf("db traefik.enable: got %q", s.Labels["traefik.enable"])
			}
		case "web":
			if s.Labels["buoy.enabled"] != "false" {
				t.Errorf("web enabled: got %q", s.Labels["buoy.enabled"])
			}
			if s.Labels["traefik.port"] != "80" {
				t.Errorf("web traefik.port: got %q", s.Labels["traefik.port"])
			}
		}
	}
}

func TestClassifySourceWithVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")

	data := []byte(`
services:
  app:
    volumes:
      - ${DOCKERDATA_DIR:-.}/app:/data:rw
      - ${BASE}/config:/config:ro
      - data:/cache
`)

	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	stacks, err := Discover(dir, 0, DefaultPatterns, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(stacks) != 1 || len(stacks[0].Services) != 1 {
		t.Fatalf("got %d stacks, %d services", len(stacks), len(stacks[0].Services))
	}

	vols := stacks[0].Services[0].Volumes
	if len(vols) != 3 {
		t.Fatalf("got %d volumes, want 3", len(vols))
	}

	findVol := func(typ, dest string) VolumeEntry {
		for _, v := range vols {
			if v.Type == typ && v.Destination == dest {
				return v
			}
		}
		t.Fatalf("volume not found: %s %s", typ, dest)
		return VolumeEntry{}
	}

	v1 := findVol("bind", "/data")
	if v1.Source != "${DOCKERDATA_DIR:-.}/app" {
		t.Errorf("source: got %q", v1.Source)
	}
	if v1.Resolved != filepath.Join(dir, "app") {
		t.Errorf("resolved: got %q, want %q", v1.Resolved, filepath.Join(dir, "app"))
	}

	v2 := findVol("bind", "/config")
	if v2.Source != "${BASE}/config" {
		t.Errorf("source: got %q", v2.Source)
	}
	if v2.Resolved != filepath.Join(dir, "${BASE}/config") {
		t.Errorf("resolved: got %q", v2.Resolved)
	}

	v3 := findVol("volume", "/cache")
	if v3.Source != "data" {
		t.Errorf("source: got %q", v3.Source)
	}
}
