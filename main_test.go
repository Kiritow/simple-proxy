package main

import (
	"slices"
	"testing"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
)

// The example is what users see first in the UI, so it must generate
// successfully without any certificate available.
func TestExampleConfig(t *testing.T) {
	cfg, err := config.Parse(exampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := new(nginx.Generator).Generate(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestVersionCommand(t *testing.T) {
	for _, c := range []struct {
		version, test string
		want          []string
	}{
		{"", "nginx -t", []string{"nginx", "-V"}},
		{"", "docker exec web nginx -t -q", []string{"docker", "exec", "web", "nginx", "-V", "-q"}},
		{"/usr/sbin/nginx -v", "nginx -t", []string{"/usr/sbin/nginx", "-v"}},
		{"", "true", nil},
		{"", "", nil},
	} {
		if got := versionCommand(c.version, c.test); !slices.Equal(got, c.want) {
			t.Errorf("versionCommand(%q, %q) = %q, want %q", c.version, c.test, got, c.want)
		}
	}
}
