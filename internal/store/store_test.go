package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/kiritow/simple-proxy/internal/store"
)

func open(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func site(id, config string) store.Site {
	return store.Site{ID: id, Config: json.RawMessage(config)}
}

// stored is a site as returned by Sites.
func stored(id, config string, generation int64) store.Site {
	return store.Site{ID: id, Config: json.RawMessage(config), Generation: generation}
}

func sites(t *testing.T, s *store.Store) []store.Site {
	t.Helper()
	got, err := s.Sites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEmpty(t *testing.T) {
	got := sites(t, open(t, filepath.Join(t.TempDir(), "sites.db")))
	if len(got) != 0 || got == nil {
		t.Errorf("Sites = %v (nil: %t), want an empty list", got, got == nil)
	}
}

func TestReplaceSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	ctx := t.Context()

	a := site("a", `{"server_name": "a.example.com", "default": {"port": 1}}`)
	b := site("b", `{"server_name": "b.example.com"}`)
	if err := s.ReplaceSites(ctx, []store.Site{a, b}); err != nil {
		t.Fatal(err)
	}

	// Configs come back minified, in order.
	want := []store.Site{
		stored("a", `{"server_name":"a.example.com","default":{"port":1}}`, 1),
		stored("b", `{"server_name":"b.example.com"}`, 1),
	}
	if got := sites(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("Sites = %+v, want %+v", got, want)
	}

	// Reorder, change b, drop a, add c.
	c := site("c", `{"server_name": "c.example.com"}`)
	b2 := site("b", `{"server_name": "b2.example.com"}`)
	if err := s.ReplaceSites(ctx, []store.Site{c, b2}); err != nil {
		t.Fatal(err)
	}
	want = []store.Site{
		stored("c", `{"server_name":"c.example.com"}`, 1),
		stored("b", `{"server_name":"b2.example.com"}`, 2),
	}
	if got := sites(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("Sites = %+v, want %+v", got, want)
	}

	// Everything persists.
	s.Close()
	if got := sites(t, open(t, path)); !reflect.DeepEqual(got, want) {
		t.Errorf("after reopening, Sites = %+v, want %+v", got, want)
	}
}

func TestReplaceSitesRejectsBadInput(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	for name, input := range map[string][]store.Site{
		"duplicate ID": {site("a", `{}`), site("a", `{}`)},
		"invalid JSON": {site("a", `{`)},
	} {
		if err := s.ReplaceSites(ctx, input); err == nil {
			t.Errorf("%s: ReplaceSites succeeded, want an error", name)
		}
	}
	if got := sites(t, s); len(got) != 0 {
		t.Errorf("after failed writes, Sites = %+v, want nothing stored", got)
	}
}

func TestUpdatedAtChangesOnlyWithConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	ctx := t.Context()
	if err := s.ReplaceSites(ctx, []store.Site{site("a", `{}`), site("b", `{}`)}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Backdate both sites, then change only b.
	if _, err := db.ExecContext(ctx, "UPDATE sites SET updated_at = 'then'"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSites(ctx, []store.Site{site("a", `{ }`), site("b", `{"gzip": true}`)}); err != nil {
		t.Fatal(err)
	}
	updated := map[string]string{}
	rows, err := db.QueryContext(context.Background(), "SELECT id, updated_at FROM sites")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatal(err)
		}
		updated[id] = at
	}
	if updated["a"] != "then" || updated["b"] == "then" {
		t.Errorf("updated_at = %v, want a unchanged and b updated", updated)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	open(t, path).Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if s, err := store.Open(t.Context(), path); err == nil {
		s.Close()
		t.Fatal("Open succeeded on a database from a newer version")
	}
}

func TestMigrationDropsRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	open(t, path).Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, tables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'revision'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 4 || tables != 0 {
		t.Errorf("schema version %d with %d revision tables, want version 4 without", version, tables)
	}
}

func TestGenerationsCountChanges(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	replace := func(sites ...store.Site) {
		t.Helper()
		if err := s.ReplaceSites(ctx, sites); err != nil {
			t.Fatal(err)
		}
	}
	generations := func() map[string]int64 {
		t.Helper()
		got := map[string]int64{}
		for _, site := range sites(t, s) {
			got[site.ID] = site.Generation
		}
		return got
	}

	replace(site("a", `{"gzip": true}`), site("b", `{}`))
	replace(site("a", `{ "gzip" : true }`), site("b", `{"gzip": true}`)) // a is unchanged
	replace(site("b", `{"gzip": false}`), site("a", `{"gzip": true}`))   // reordered
	if got := generations(); got["a"] != 1 || got["b"] != 3 {
		t.Errorf("generations = %v, want a at 1 and b at 3", got)
	}
	// Generations given to ReplaceSites are ignored.
	replace(store.Site{ID: "a", Config: json.RawMessage(`{"gzip": true}`), Generation: 9}, site("b", `{"gzip": false}`))
	if got := generations(); got["a"] != 1 {
		t.Errorf("generations = %v, want a still at 1", got)
	}
}

func TestCerts(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	a := store.Cert{Name: "a.example.com", CertPEM: []byte("cert a"), KeyPEM: []byte("key a")}
	b := store.Cert{Name: "b.example.com", CertPEM: []byte("cert b"), KeyPEM: []byte("key b"), CA: "Root", KeyType: "ec", Days: 90}
	if err := s.ReplaceCerts(ctx, []store.Cert{b, a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Certs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a.example.com" || got[1].CA != "Root" || got[1].Days != 90 ||
		string(got[1].KeyPEM) != "key b" || got[0].CreatedAt.IsZero() {
		t.Fatalf("Certs = %+v, want a and b by name", got)
	}

	// Replacing b keeps its creation time; dropping a deletes it.
	b.CertPEM = []byte("cert b2")
	if err := s.Replace(ctx, nil, []store.Cert{b}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Certs(ctx)
	if len(got) != 1 || string(got[0].CertPEM) != "cert b2" || got[0].CreatedAt.IsZero() {
		t.Errorf("Certs = %+v, want b replaced", got)
	}
	if err := s.ReplaceCerts(ctx, []store.Cert{a, a}); err == nil {
		t.Error("ReplaceCerts accepted duplicate names")
	}
}

func TestAuthorities(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	root := store.Authority{Name: "Home Root CA", CertPEM: []byte("cert"), KeyPEM: []byte("key")}
	if err := s.AddAuthority(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuthority(ctx, root); !errors.Is(err, store.ErrExists) {
		t.Errorf("adding it again: %v, want ErrExists", err)
	}
	got, err := s.Authorities(ctx)
	if err != nil || len(got) != 1 || got[0].Name != root.Name || string(got[0].KeyPEM) != "key" {
		t.Fatalf("Authorities = %+v, %v", got, err)
	}
	if err := s.DeleteAuthority(ctx, root.Name); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAuthority(ctx, root.Name); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "sites.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	open(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the database has permissions %v, want -rw-------", perm)
	}
}
