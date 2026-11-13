// Command simple-proxy serves a web UI and an HTTP API that generate nginx
// reverse proxy configuration from a YAML description of virtual hosts.
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
	"syscall"
	"time"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/nginx"
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
	acmeBin := flags.String("acme", defaultAcmeBin(), "`path` of the acme.sh script used to look up certificates")
	fallbackCert := flags.String("fallback-cert", nginx.DefaultFallbackCert, "`path` of the certificate presented to requests for unknown hosts")
	fallbackKey := flags.String("fallback-key", nginx.DefaultFallbackKey, "`path` of the private key of -fallback-cert")
	_ = flags.Parse(args) // ExitOnError: Parse exits instead of returning errors

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	locator := acme.NewLocator(*acmeBin)
	gen := &nginx.Generator{
		Certs:        locator,
		FallbackCert: *fallbackCert,
		FallbackKey:  *fallbackKey,
	}
	srv := &http.Server{
		Handler:           web.NewHandler(gen, locator, exampleConfig, logger),
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

func defaultAcmeBin() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "acme.sh"
	}
	return filepath.Join(home, ".acme.sh", "acme.sh")
}
