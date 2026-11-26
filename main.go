// Command simple-proxy manages nginx as a reverse proxy: sites configured in
// its web UI or HTTP API are stored, and applied to nginx as they change.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/buildinfo"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/manager"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
	"github.com/kiritow/simple-proxy/internal/web"
)

//go:embed config.yaml.example
var exampleConfig []byte

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "simple-proxy:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("simple-proxy", flag.ExitOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", "127.0.0.1:8080", "`address` to serve HTTP on")
	dbPath := flags.String("db", defaultDBPath(), "`path` of the SQLite database storing the sites, certificates and CAs")
	certDir := flags.String("cert-dir", "", "`directory` where uploaded and signed certificates are written for nginx (default: certs next to -db)")
	nginxConf := flags.String("nginx-conf", "", "`path` of the nginx configuration file to manage, e.g. /etc/nginx/conf.d/proxy.conf; if empty, sites are only stored")
	nginxTest := flags.String("nginx-test", "nginx -t", "`command` testing nginx's configuration")
	nginxReload := flags.String("nginx-reload", "nginx -s reload", "`command` reloading nginx")
	nginxVersion := flags.String("nginx-version", "", "`command` printing nginx's version and build, shown in the UI (default: -nginx-test with -V for -t)")
	acmeBin := flags.String("acme", defaultAcmeBin(), "`path` of the acme.sh script managing certificates")
	acmeWebroot := flags.String("acme-webroot", "", "`directory` served at /.well-known/acme-challenge/ to issue certificates over HTTP; if empty, only DNS validation is available")
	fallbackCert := flags.String("fallback-cert", nginx.DefaultFallbackCert, "`path` of the certificate presented to requests for unknown hosts")
	fallbackKey := flags.String("fallback-key", nginx.DefaultFallbackKey, "`path` of the private key of -fallback-cert")
	_ = flags.Parse(args) // ExitOnError: Parse exits instead of returning errors

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	build := buildinfo.Read()
	logger.Info("starting simple-proxy", "commit", build.Commit, "modified", build.Modified)
	if *certDir == "" {
		*certDir = filepath.Join(filepath.Dir(*dbPath), "certs")
	}
	// nginx resolves relative paths against its own prefix.
	absCertDir, err := filepath.Abs(*certDir)
	if err != nil {
		return err
	}
	sites, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer sites.Close()
	logger.Info("opened database", "path", *dbPath)

	gen := &nginx.Generator{
		FallbackCert: *fallbackCert,
		FallbackKey:  *fallbackKey,
		ACMEWebroot:  *acmeWebroot,
	}
	opts := manager.Options{
		Store:       sites,
		Generator:   gen,
		CertDir:     certs.Dir(absCertDir),
		ACMEWebroot: *acmeWebroot,
		RenewHook:   *nginxReload,
		Logger:      logger,
	}
	if _, err := os.Stat(*acmeBin); err == nil {
		ca := acme.NewClient(*acmeBin)
		gen.Certs, opts.CA = ca, ca
	} else {
		logger.Warn("acme.sh not found: only uploaded and signed certificates are available", "path", *acmeBin)
	}
	if *acmeWebroot != "" {
		if err := os.MkdirAll(*acmeWebroot, 0o755); err != nil {
			return err
		}
	}
	if *nginxConf != "" {
		opts.Applier = &apply.Applier{
			Path:    *nginxConf,
			Test:    strings.Fields(*nginxTest),
			Reload:  strings.Fields(*nginxReload),
			Version: versionCommand(*nginxVersion, *nginxTest),
		}
	}

	m := manager.New(opts)
	if opts.Applier == nil {
		logger.Info("not managing nginx: set -nginx-conf to apply sites")
	} else if err := m.Apply(ctx); err != nil {
		logger.Warn("applying the stored sites failed", "err", err)
	} else {
		logger.Info("applied the stored sites", "path", *nginxConf)
	}

	srv := &http.Server{
		Handler: web.NewHandler(web.Options{
			Manager: m,
			Example: exampleConfig,
			Logger:  logger,
			Build:   build,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	logger.Info("serving", "url", "http://"+ln.Addr().String())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// defaultDBPath returns sites.db in the directory of the executable, so that
// the data stays next to the program wherever it is started from.
func defaultDBPath() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "sites.db"
	}
	return filepath.Join(filepath.Dir(exe), "sites.db")
}

// versionCommand returns the command printing nginx's version: version if
// set, otherwise the test command with -V for -t, so that it asks the same
// nginx, e.g. through "docker exec". Without either, it returns nil.
func versionCommand(version, test string) []string {
	if version != "" {
		return strings.Fields(version)
	}
	cmd := strings.Fields(test)
	i := slices.Index(cmd, "-t")
	if i < 0 {
		return nil
	}
	cmd[i] = "-V"
	return cmd
}

func defaultAcmeBin() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "acme.sh"
	}
	return filepath.Join(home, ".acme.sh", "acme.sh")
}
