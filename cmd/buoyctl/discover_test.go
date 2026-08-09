package main

import (
	"reflect"
	"testing"
)

func TestCollapseMounts(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "collapse variable root",
			in:   []string{"${DOCKERDATA_DIR:-.}/beszel", "${DOCKERDATA_DIR:-.}/gotify", "${DOCKERDATA_DIR:-.}/uptime-kuma"},
			want: []string{"${DOCKERDATA_DIR:-.}"},
		},
		{
			name: "single variable path keeps full form",
			in:   []string{"${DOCKERDATA_DIR:-.}/beszel"},
			want: []string{"${DOCKERDATA_DIR:-.}/beszel"},
		},
		{
			name: "collapse absolute ancestor, keep unrelated",
			in:   []string{"/media/n/a", "/media/n/b", "/opt/z"},
			want: []string{"/media/n", "/opt/z"},
		},
		{
			name: "unrelated paths stay",
			in:   []string{"/a/x", "/b/y"},
			want: []string{"/a/x", "/b/y"},
		},
		{
			name: "nested paths collapse to outermost",
			in:   []string{"/data/x", "/data/x/y", "/data/z"},
			want: []string{"/data"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseMounts(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLongestCommonPathPrefix(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"/media/n/a", "/media/n/b", "/media/n"},
		{"/a/x", "/b/y", ""},
		{"${DOCKERDATA_DIR:-.}/beszel", "${DOCKERDATA_DIR:-.}/gotify", "${DOCKERDATA_DIR:-.}"},
		{"/same", "/same", "/same"},
	}
	for _, tt := range tests {
		if got := longestCommonPathPrefix(tt.a, tt.b); got != tt.want {
			t.Errorf("longestCommonPathPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}
