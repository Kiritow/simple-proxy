package main

import (
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
