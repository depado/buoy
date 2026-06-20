package registry

import (
	"testing"
)

func TestIsLocalPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"absolute", "/backup/repo", true},
		{"relative dot", "./backup/repo", true},
		{"relative dotdot", "../backup/repo", true},
		{"no colon plain", "backup/repo", true},
		{"s3 remote", "s3:s3.amazonaws.com/bucket", false},
		{"b2 remote", "b2:bucketname:path", false},
		{"sftp remote", "sftp:user@host:path", false},
		{"rest remote", "rest:https://host:8000/", false},
		{"gs remote", "gs:bucket:path", false},
		{"azure remote", "azure:container:path", false},
		{"rclone remote", "rclone:remote:path", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalPath(tt.path)
			if got != tt.want {
				t.Errorf("isLocalPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
