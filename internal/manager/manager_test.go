package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/manager"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
)

// fakeCA knows the certificate "*.example.com" and records requests.
type fakeCA struct{ issued []acme.IssueRequest }

func (f *fakeCA) LocateCert(_ context.Context, name string) (nginx.Certificate, error) {
	if name != "*.example.com" {
		return nginx.Certificate{}, fmt.Errorf("%w: %s", nginx.ErrCertNotFound, name)
	}
	return nginx.Certificate{Fullchain: "/c/full.cer", Key: "/c/key", CA: "/c/ca.cer"}, nil
}

func (f *fakeCA) Certs(context.Context) ([]acme.Cert, error) {
	return []acme.Cert{{Name: "*.example.com", Domains: []string{"*.example.com"}}}, nil
}

func (f *fakeCA) Issue(_ context.Context, req acme.IssueRequest) (string, error) {
	f.issued = append(f.issued, req)
	return "Cert success.", nil
}

type env struct {
	m        *manager.Manager
	store    *store.Store
	ca       *fakeCA
	conf     string // the file nginx includes
	reloads  string // a file logging reloads
	certDir  string
	failFlag string // nginx rejects everything while this file exists
}

// fakeNginxTest rejects configurations containing "reject_me", or while
// the file $0.fail exists, or referring to missing PEM files.
const fakeNginxTest = `if grep -q reject_me "$0" || [ -e "$0.fail" ]; then echo "nginx: [emerg] unknown directive" >&2; exit 1; fi
for f in $(grep -o '"[^"]*\.pem"' "$0" | tr -d '"'); do [ -r "$f" ] || { echo "nginx: [emerg] cannot load $f" >&2; exit 1; }; done`

// newEnv returns a Manager applying to a fake nginx, see fakeNginxTest.
func newEnv(t *testing.T) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	s, err := store.Open(t.Context(), filepath.Join(dir, "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	e := &env{
		store:    s,
		ca:       &fakeCA{},
		conf:     filepath.Join(dir, "proxy.conf"),
		reloads:  filepath.Join(dir, "reloads"),
		certDir:  filepath.Join(dir, "certs"),
		failFlag: filepath.Join(dir, "proxy.conf.fail"),
	}
	e.m = manager.New(manager.Options{
		Store:     s,
		Generator: &nginx.Generator{Certs: e.ca},
		CertDir:   certs.Dir(e.certDir),
		Applier: &apply.Applier{
			Path:   e.conf,
			Test:   []string{"sh", "-c", fakeNginxTest, e.conf},
			Reload: []string{"sh", "-c", `echo reload >> "$0"`, e.reloads},
		},
		CA:          e.ca,
		ACMEWebroot: "/var/www/acme",
		RenewHook:   "nginx -s reload",
	})
	return e
}

func (e *env) nginxConf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.conf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func (e *env) stored(t *testing.T) []store.Site {
	t.Helper()
	sites, err := e.store.Sites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

func site(name string, port int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"server_name": %q, "default": {"port": %d}}`, name, port))
}

func fields(err error) []string {
	var errs config.FieldErrors
	var one *config.FieldError
	switch {
	case errors.As(err, &errs):
	case errors.As(err, &one):
		errs = config.FieldErrors{one}
	}
	var out []string
	for _, fe := range errs {
		out = append(out, fe.Error())
	}
	return out
}

func TestCreateUpdateDelete(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	a, err := e.m.CreateSite(ctx, site("a.example.com", 3000))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.nginxConf(t), "proxy_pass http://127.0.0.1:3000;") {
		t.Fatalf("the site was not applied:\n%s", e.nginxConf(t))
	}
	if st := e.m.Status(); st.AppliedAt == nil || st.Error != "" {
		t.Errorf("Status = %+v, want applied", st)
	}

	if _, err := e.m.UpdateSite(ctx, a.ID, a.Generation, site("a.example.com", 4000)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.nginxConf(t), "proxy_pass http://127.0.0.1:4000;") {
		t.Fatalf("the update was not applied:\n%s", e.nginxConf(t))
	}
	if got := e.stored(t); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("stored %v, want the updated site", got)
	}

	if err := e.m.DeleteSite(ctx, a.ID, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.nginxConf(t), "a.example.com") || len(e.stored(t)) != 0 {
		t.Error("the site was not deleted")
	}
	reloads, _ := os.ReadFile(e.reloads)
	if n := strings.Count(string(reloads), "reload"); n != 3 {
		t.Errorf("nginx reloaded %d times, want 3", n)
	}

	for _, err := range []error{
		e.m.DeleteSite(ctx, "missing", 0),
		func() error { _, err := e.m.UpdateSite(ctx, "missing", 0, site("x.example.com", 1)); return err }(),
	} {
		if !errors.Is(err, manager.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	}
}

func TestRejectedChangesChangeNothing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a, err := e.m.CreateSite(ctx, site("a.example.com", 3000))
	if err != nil {
		t.Fatal(err)
	}
	before := e.nginxConf(t)

	for _, tc := range []struct {
		name   string
		config string
		want   string // in the error
	}{
		{"invalid", `{"server_name": "b;", "default": {"port": 1}}`, `servers[0].server_name: invalid name "b;"`},
		{"domain in use", `{"server_name": "b.example.com A.example.com", "default": {"port": 1}}`, `servers[0].server_name: "A.example.com" is used by another site`},
		{"missing certificate", `{"server_name": "b.example.com", "https": true, "default": {"port": 1}}`, "servers[0].cert_name: certificate not found"},
		{"rejected by nginx", `{"server_name": "b.example.com", "options": ["reject_me on"], "default": {"port": 1}}`, "[emerg] unknown directive"},
		{"unknown field", `{"server_name": "b.example.com", "websockets": true}`, "field websockets not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.m.CreateSite(ctx, json.RawMessage(tc.config))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CreateSite error = %v, want %q", err, tc.want)
			}
			if got := e.stored(t); len(got) != 1 || got[0].ID != a.ID {
				t.Errorf("stored %v, want only the first site", got)
			}
			if e.nginxConf(t) != before {
				t.Error("the nginx configuration changed")
			}
		})
	}

	var aerr *apply.Error
	_, err = e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "b.example.com", "options": ["reject_me on"], "default": {"port": 1}}`))
	if !errors.As(err, &aerr) || aerr.Step != "test" {
		t.Errorf("error = %v, want an *apply.Error from the test", err)
	}
	if st := e.m.Status(); st.Error != "" {
		t.Errorf("Status.Error = %q after a rejected change, want none: nginx runs the stored sites", st.Error)
	}
}

func TestDisabledSitesMayShareDomains(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.m.CreateSite(ctx, site("a.example.com", 3000)); err != nil {
		t.Fatal(err)
	}
	b, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "a.example.com", "enable": false, "default": {"port": 4000}}`))
	if err != nil {
		t.Fatalf("a disabled site was rejected: %v", err)
	}
	// Enabling it conflicts.
	_, err = e.m.UpdateSite(ctx, b.ID, b.Generation, site("a.example.com", 4000))
	if got := fields(err); len(got) != 1 || !strings.Contains(got[0], "is used by another site") {
		t.Errorf("enabling a duplicate: %v", err)
	}
}

func TestReplaceSites(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.m.CreateSite(ctx, site("old.example.com", 3000)); err != nil {
		t.Fatal(err)
	}
	_, err := e.m.ReplaceSites(ctx, []json.RawMessage{site("a.example.com", 1), site("a.example.com", 2)})
	if got := fields(err); len(got) != 1 || !strings.HasPrefix(got[0], "servers[1].server_name") {
		t.Fatalf("duplicate import: %v", err)
	}
	sites, err := e.m.ReplaceSites(ctx, []json.RawMessage{site("a.example.com", 1), site("b.example.com", 2)})
	if err != nil || len(sites) != 2 {
		t.Fatalf("ReplaceSites = %v, %v", sites, err)
	}
	conf := e.nginxConf(t)
	if strings.Contains(conf, "old.example.com") || !strings.Contains(conf, "b.example.com") {
		t.Errorf("the import was not applied:\n%s", conf)
	}
}

func TestInvalidStoredSitesAreSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	// An incomplete draft stored by an earlier version.
	draft := store.Site{ID: "draft", Config: json.RawMessage(`{"server_name": "", "default": {}}`)}
	good := store.Site{ID: "good", Config: site("good.example.com", 3000)}
	if err := e.store.ReplaceSites(ctx, []store.Site{draft, good}); err != nil {
		t.Fatal(err)
	}
	infos, err := e.m.Sites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if infos[0].Problem == nil || infos[1].Problem != nil {
		t.Errorf("problems = %v, %v; want only the draft flagged", infos[0].Problem, infos[1].Problem)
	}
	if err := e.m.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.nginxConf(t), "good.example.com") {
		t.Error("the valid site was not applied")
	}
	// Other sites can still be changed.
	if _, err := e.m.CreateSite(ctx, site("new.example.com", 4000)); err != nil {
		t.Errorf("CreateSite: %v", err)
	}
}

func TestApplyFailureIsReported(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	// Something else broke the stored sites behind the manager's back.
	if err := e.store.ReplaceSites(ctx, []store.Site{{ID: "x", Config: json.RawMessage(`{"server_name": "x.example.com", "options": ["reject_me on"], "default": {"port": 1}}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Apply(ctx); err == nil {
		t.Fatal("Apply succeeded")
	}
	st := e.m.Status()
	if !strings.Contains(st.Error, "test failed") || !strings.Contains(st.Output, "[emerg]") {
		t.Errorf("Status = %+v, want the failed test and its output", st)
	}
}

func TestWithoutApplier(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.Context(), filepath.Join(dir, "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := manager.New(manager.Options{Store: s, Generator: &nginx.Generator{}})
	if _, err := m.CreateSite(t.Context(), site("a.example.com", 3000)); err != nil {
		t.Fatal(err)
	}
	conf, err := m.NginxConfig(t.Context())
	if err != nil || !strings.Contains(string(conf), "a.example.com") {
		t.Errorf("NginxConfig = %s, %v", conf, err)
	}
	if st := m.Status(); st.Applies || st.ACME {
		t.Errorf("Status = %+v, want nothing applied and no acme.sh", st)
	}
	if list, err := m.Certificates(t.Context()); err != nil || len(list.Certificates) != 0 || list.ACMEError != "" {
		t.Errorf("Certificates = %+v, %v; want none", list, err)
	}
	if _, err := m.IssueCertificate(t.Context(), []string{"a.example.com"}, "dns_cf"); !errors.Is(err, manager.ErrNoACME) {
		t.Errorf("IssueCertificate error = %v, want ErrNoACME", err)
	}
}

func TestIssueCertificate(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.m.IssueCertificate(ctx, []string{"a.example.com"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.IssueCertificate(ctx, []string{"*.example.org"}, "dns_cf"); err != nil {
		t.Fatal(err)
	}
	want := []acme.IssueRequest{
		{Domains: []string{"a.example.com"}, Webroot: "/var/www/acme", RenewHook: "nginx -s reload"},
		{Domains: []string{"*.example.org"}, DNS: "dns_cf", RenewHook: "nginx -s reload"},
	}
	if fmt.Sprint(e.ca.issued) != fmt.Sprint(want) {
		t.Errorf("issued %+v, want %+v", e.ca.issued, want)
	}
}

func TestIssueCertificateWithoutWebroot(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.Context(), filepath.Join(dir, "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := manager.New(manager.Options{Store: s, Generator: &nginx.Generator{}, CA: &fakeCA{}})
	if _, err := m.IssueCertificate(t.Context(), []string{"a.example.com"}, ""); !errors.Is(err, manager.ErrNoWebroot) {
		t.Errorf("error = %v, want ErrNoWebroot", err)
	}
}

func TestGenerations(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a, err := e.m.CreateSite(ctx, site("a.example.com", 3000))
	if err != nil || a.Generation != 1 {
		t.Fatalf("CreateSite = %+v, %v; want generation 1", a, err)
	}

	// Two windows opened a at generation 1. The first one saves...
	first, err := e.m.UpdateSite(ctx, a.ID, 1, site("a.example.com", 3001))
	if err != nil || first.Generation != 2 {
		t.Fatalf("UpdateSite = %+v, %v; want generation 2", first, err)
	}
	// ...and the second one cannot overwrite it, nor delete the site.
	if _, err := e.m.UpdateSite(ctx, a.ID, 1, site("a.example.com", 3002)); !errors.Is(err, manager.ErrConflict) {
		t.Errorf("stale UpdateSite error = %v, want ErrConflict", err)
	}
	if err := e.m.DeleteSite(ctx, a.ID, 1); !errors.Is(err, manager.ErrConflict) {
		t.Errorf("stale DeleteSite error = %v, want ErrConflict", err)
	}
	if !strings.Contains(e.nginxConf(t), "127.0.0.1:3001") {
		t.Error("a stale change was applied")
	}

	// Saving an unchanged site keeps its generation.
	same, err := e.m.UpdateSite(ctx, a.ID, 2, site("a.example.com", 3001))
	if err != nil || same.Generation != 2 {
		t.Errorf("unchanged UpdateSite = %+v, %v; want generation 2", same, err)
	}
	// Generation 0 overwrites whatever is stored.
	if _, err := e.m.UpdateSite(ctx, a.ID, 0, site("a.example.com", 3003)); err != nil {
		t.Errorf("unconditional UpdateSite: %v", err)
	}
	if err := e.m.DeleteSite(ctx, a.ID, 3); err != nil {
		t.Errorf("DeleteSite at the current generation: %v", err)
	}
}

func TestPreview(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a, err := e.m.CreateSite(ctx, site("a.example.com", 3000))
	if err != nil {
		t.Fatal(err)
	}
	before := e.nginxConf(t)

	p, err := e.m.Preview(ctx, a.ID, site("a.example.com", 4000))
	if err != nil {
		t.Fatal(err)
	}
	if p.Generation != 1 {
		t.Errorf("Generation = %d, want 1", p.Generation)
	}
	cur, prop := string(p.Current), string(p.Proposed)
	if !strings.Contains(cur, "127.0.0.1:3000") || !strings.Contains(prop, "127.0.0.1:4000") || strings.Contains(prop, "127.0.0.1:3000") {
		t.Errorf("preview does not show the change:\n%s\n---\n%s", cur, prop)
	}
	// Both are generated at the same time, so only the change differs.
	if strings.Replace(cur, "3000", "4000", 1) != prop {
		t.Errorf("the preview differs in more than the change:\n%s\n---\n%s", cur, prop)
	}

	// A new site.
	p, err = e.m.Preview(ctx, "", site("b.example.com", 5000))
	if err != nil || p.Generation != 0 || !strings.Contains(string(p.Proposed), "server_name b.example.com;") || strings.Contains(string(p.Current), "b.example.com") {
		t.Errorf("new site preview = %+v, %v", p, err)
	}

	// Problems are found like when saving, domain conflicts included.
	if _, err := e.m.Preview(ctx, "", site("A.example.com", 1)); len(fields(err)) != 1 || !strings.Contains(fields(err)[0], "is used by another site") {
		t.Errorf("conflicting preview error = %v", err)
	}
	if _, err := e.m.Preview(ctx, "missing", site("x.example.com", 1)); !errors.Is(err, manager.ErrNotFound) {
		t.Errorf("preview of a missing site: %v, want ErrNotFound", err)
	}

	// Nothing was changed.
	if e.nginxConf(t) != before || len(e.stored(t)) != 1 {
		t.Error("Preview changed something")
	}
}
