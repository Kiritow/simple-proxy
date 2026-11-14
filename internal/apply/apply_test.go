package apply_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/apply"
)

// The fake nginx accepts configurations without the word "bad".
func newApplier(t *testing.T, reload []string) (*apply.Applier, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "proxy.conf")
	test := []string{"sh", "-c", `if grep -q bad "$0"; then echo "nginx: [emerg] bad config" >&2; exit 1; fi`, path}
	return &apply.Applier{Path: path, Test: test, Reload: reload}, path
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApply(t *testing.T) {
	a, path := newApplier(t, []string{"true"})
	if err := a.Apply(t.Context(), []byte("good 1")); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(t.Context(), []byte("good 2")); err != nil {
		t.Fatal(err)
	}
	if got := content(t, path); got != "good 2" {
		t.Errorf("file = %q, want %q", got, "good 2")
	}
}

func TestApplyKeepsTheFile(t *testing.T) {
	// The file is rewritten in place, so bind mounts of it keep working.
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("good 1")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := a.Apply(t.Context(), []byte("good 2")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Error("Apply replaced the file instead of rewriting it")
	}
}

func TestApplyRestoresAfterFailedTest(t *testing.T) {
	a, path := newApplier(t, []string{"true"})
	if err := a.Apply(t.Context(), []byte("good")); err != nil {
		t.Fatal(err)
	}
	err := a.Apply(t.Context(), []byte("bad"))
	var aerr *apply.Error
	if !errors.As(err, &aerr) || aerr.Step != "test" {
		t.Fatalf("Apply error = %v, want a test failure", err)
	}
	if !strings.Contains(aerr.Output, "[emerg] bad config") {
		t.Errorf("Output = %q, want nginx's message", aerr.Output)
	}
	if got := content(t, path); got != "good" {
		t.Errorf("file = %q, want the previous configuration", got)
	}
}

func TestApplyRemovesNewFileAfterFailedTest(t *testing.T) {
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("bad")); err == nil {
		t.Fatal("Apply succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the rejected file is still there: %v", err)
	}
}

func TestApplyRestoresAfterFailedReload(t *testing.T) {
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("good")); err != nil {
		t.Fatal(err)
	}
	a.Reload = []string{"sh", "-c", "echo 'nginx: [error] open() \"/run/nginx.pid\" failed'; exit 1"}
	err := a.Apply(t.Context(), []byte("good 2"))
	var aerr *apply.Error
	if !errors.As(err, &aerr) || aerr.Step != "reload" || !strings.Contains(aerr.Output, "nginx.pid") {
		t.Fatalf("Apply error = %v, want a reload failure with its output", err)
	}
	if got := content(t, path); got != "good" {
		t.Errorf("file = %q, want the previous configuration", got)
	}
}
