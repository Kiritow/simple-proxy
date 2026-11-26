// Package apply installs nginx configuration: it writes the configuration
// file, has nginx test it and reloads nginx, restoring the previous file if
// any of this fails.
package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds each test and reload command.
const commandTimeout = 30 * time.Second

// Applier installs configuration into a file that nginx includes.
type Applier struct {
	// Path is the configuration file, e.g. /etc/nginx/conf.d/proxy.conf.
	Path string
	// Test checks nginx's configuration, e.g. ["nginx", "-t"]. Skipped if
	// empty.
	Test []string
	// Reload makes nginx load the configuration, e.g. ["nginx", "-s",
	// "reload"]. Skipped if empty.
	Reload []string
	// Version prints nginx's version and build, e.g. ["nginx", "-V"]. nginx
	// is not described if empty.
	Version []string
}

// Nginx describes the nginx that configuration is applied to, as nginx -V
// does.
type Nginx struct {
	Version string `json:"version"` // such as "nginx/1.27.3"
	// Build is the rest of the output, such as the OpenSSL nginx was built
	// with, and its configure arguments.
	Build string `json:"build,omitempty"`
}

// ErrNoVersion reports that the Applier has no Version command.
var ErrNoVersion = errors.New("no command printing nginx's version")

// Nginx runs the Version command and describes nginx from its output.
func (a *Applier) Nginx(ctx context.Context) (Nginx, error) {
	if len(a.Version) == 0 {
		return Nginx{}, ErrNoVersion
	}
	out, err := run(ctx, a.Version)
	if err != nil {
		if out != "" {
			err = fmt.Errorf("%w\n%s", err, out)
		}
		return Nginx{}, err
	}
	// The first line is like "nginx version: nginx/1.27.3", or
	// "nginx version: openresty/1.25.3.1".
	first, rest, _ := strings.Cut(out, "\n")
	_, version, ok := strings.Cut(first, " version: ")
	if !ok || strings.TrimSpace(version) == "" {
		return Nginx{}, fmt.Errorf("%s printed no version: %q", strings.Join(a.Version, " "), first)
	}
	return Nginx{Version: strings.TrimSpace(version), Build: strings.TrimSpace(rest)}, nil
}

// Error reports a failed step of Apply.
type Error struct {
	Step   string // "write", "test" or "reload"
	Output string // of the step's command
	Err    error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s failed: %v", e.Step, e.Err)
	if e.Output != "" {
		msg += "\n" + e.Output
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Apply installs conf. If the test or the reload fails, it restores the
// previous file, so that the file keeps holding configuration that nginx
// accepted.
func (a *Applier) Apply(ctx context.Context, conf []byte) error {
	previous, err := os.ReadFile(a.Path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &Error{Step: "write", Err: err}
	}
	// Write in place rather than replacing the file, so that bind mounts of
	// the file itself into an nginx container keep seeing it. nginx only
	// reads it when tested or reloaded below, after the write.
	if err := os.WriteFile(a.Path, conf, 0o644); err != nil {
		return &Error{Step: "write", Err: err}
	}

	restore := func(failure *Error) error {
		var err error
		if existed {
			err = os.WriteFile(a.Path, previous, 0o644)
		} else {
			err = os.Remove(a.Path)
		}
		if err != nil {
			failure.Err = errors.Join(failure.Err, fmt.Errorf("restoring %s: %w", a.Path, err))
		}
		return failure
	}

	if out, err := run(ctx, a.Test); err != nil {
		return restore(&Error{Step: "test", Output: out, Err: err})
	}
	if out, err := run(ctx, a.Reload); err != nil {
		return restore(&Error{Step: "reload", Output: out, Err: err})
	}
	return nil
}

// run runs cmd and returns its combined output.
func run(ctx context.Context, cmd []string) (string, error) {
	if len(cmd) == 0 {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput()
	out = bytes.TrimSpace(out)
	if err != nil {
		err = fmt.Errorf("%s: %w", strings.Join(cmd, " "), err)
	}
	return string(out), err
}
