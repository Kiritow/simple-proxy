package store_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"testing"
	"time"

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

// sites returns the stored sites without their times, which it checks are
// set.
func sites(t *testing.T, s *store.Store) []store.Site {
	t.Helper()
	got, err := s.Sites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, site := range got {
		if site.CreatedAt.IsZero() || site.UpdatedAt.IsZero() {
			t.Errorf("site %q has no times: %+v", site.ID, site)
		}
		got[i].CreatedAt, got[i].UpdatedAt = time.Time{}, time.Time{}
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
	before := time.Now().Truncate(time.Second)
	if err := s.ReplaceSites(ctx, []store.Site{site("a", `{}`), site("b", `{}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range got {
		if site.CreatedAt.Before(before) || !site.UpdatedAt.Equal(site.CreatedAt) {
			t.Errorf("new site %q: created %v, updated %v; want both since %v", site.ID, site.CreatedAt, site.UpdatedAt, before)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Backdate both sites, then change only b.
	then := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := db.ExecContext(ctx, "UPDATE sites SET created_at = ?1, updated_at = ?1", then.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSites(ctx, []store.Site{site("a", `{ }`), site("b", `{"gzip": true}`)}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Sites(ctx); err != nil {
		t.Fatal(err)
	}
	for _, site := range got {
		if !site.CreatedAt.Equal(then) {
			t.Errorf("site %q: created %v, want %v kept", site.ID, site.CreatedAt, then)
		}
	}
	if a, b := got[0], got[1]; !a.UpdatedAt.Equal(then) || b.UpdatedAt.Before(before) {
		t.Errorf("updated a at %v, b at %v; want a unchanged and b updated", a.UpdatedAt, b.UpdatedAt)
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
	if version != 5 || tables != 0 {
		t.Errorf("schema version %d with %d revision tables, want version 5 without", version, tables)
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewID(t *testing.T) {
	a, b := store.NewID(), store.NewID()
	if !uuidV4.MatchString(a) || a == b {
		t.Errorf("NewID = %q, %q; want distinct version 4 UUIDs", a, b)
	}
}

// TestMigrationAddsIDs upgrades a database of version 4, whose certificates
// and CAs have no IDs.
func TestMigrationAddsIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sites (id TEXT PRIMARY KEY, position INTEGER NOT NULL, config TEXT NOT NULL CHECK (json_valid(config)),
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 1) STRICT`,
		`CREATE TABLE certificates (name TEXT PRIMARY KEY, cert TEXT NOT NULL, key TEXT NOT NULL, ca TEXT NOT NULL,
			key_type TEXT NOT NULL, days INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL) STRICT`,
		`CREATE TABLE authorities (name TEXT PRIMARY KEY, cert TEXT NOT NULL, key TEXT NOT NULL, created_at TEXT NOT NULL) STRICT`,
		`INSERT INTO certificates VALUES
			('a', 'cert a', 'key a', '', '', 0, '2026-01-02T03:04:05Z', '2026-01-02T03:04:05Z'),
			('b', 'cert b', 'key b', 'Root', 'ec', 90, '2026-01-02T03:04:05Z', '2026-02-02T03:04:05Z')`,
		`INSERT INTO authorities VALUES ('Root', 'cert', 'key', '2026-01-01T00:00:00Z')`,
		`PRAGMA user_version = 4`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s := open(t, path)
	certs, err := s.Certs(t.Context())
	if err != nil || len(certs) != 2 {
		t.Fatalf("Certs = %+v, %v", certs, err)
	}
	a, b := certs[0], certs[1]
	if !uuidV4.MatchString(a.ID) || !uuidV4.MatchString(b.ID) || a.ID == b.ID {
		t.Errorf("IDs %q and %q, want distinct version 4 UUIDs", a.ID, b.ID)
	}
	if string(b.KeyPEM) != "key b" || b.CA != "Root" || b.Days != 90 || b.UpdatedAt.Month() != 2 {
		t.Errorf("certificate b = %+v, want it kept", b)
	}
	cas, err := s.Authorities(t.Context())
	if err != nil || len(cas) != 1 || !uuidV4.MatchString(cas[0].ID) || cas[0].Name != "Root" {
		t.Errorf("Authorities = %+v, %v", cas, err)
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
	a := store.Cert{ID: store.NewID(), Name: "a.example.com", CertPEM: []byte("cert a"), KeyPEM: []byte("key a")}
	b := store.Cert{ID: store.NewID(), Name: "b.example.com", CertPEM: []byte("cert b"), KeyPEM: []byte("key b"), CA: "Root", KeyType: "ec", Days: 90}
	if err := s.ReplaceCerts(ctx, []store.Cert{b, a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Certs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a.example.com" || got[0].ID != a.ID || got[1].CA != "Root" || got[1].Days != 90 ||
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
	created := got[0].CreatedAt

	// Renaming b to the name of a deleted certificate keeps the rest.
	b.Name = "a.example.com"
	if err := s.Replace(ctx, nil, []store.Cert{b}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Certs(ctx)
	if len(got) != 1 || got[0].ID != b.ID || got[0].Name != "a.example.com" || string(got[0].CertPEM) != "cert b2" || !got[0].CreatedAt.Equal(created) {
		t.Errorf("Certs = %+v, want b renamed", got)
	}

	for _, bad := range [][]store.Cert{
		{a, a},
		{a, {ID: store.NewID(), Name: a.Name}},
		{{Name: "no ID"}},
	} {
		if err := s.ReplaceCerts(ctx, bad); err == nil {
			t.Errorf("ReplaceCerts accepted %+v", bad)
		}
	}
}

func TestAuthorities(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	root := store.Authority{ID: store.NewID(), Name: "Home Root CA", CertPEM: []byte("cert"), KeyPEM: []byte("key")}
	if err := s.AddAuthority(ctx, root); err != nil {
		t.Fatal(err)
	}
	again := root
	again.ID = store.NewID()
	if err := s.AddAuthority(ctx, again); !errors.Is(err, store.ErrExists) {
		t.Errorf("adding it again: %v, want ErrExists", err)
	}
	got, err := s.Authorities(ctx)
	if err != nil || len(got) != 1 || got[0].Name != root.Name || got[0].ID != root.ID || string(got[0].KeyPEM) != "key" {
		t.Fatalf("Authorities = %+v, %v", got, err)
	}

	// Renaming it renames the CA of the certificates it signed.
	signed := []store.Cert{
		{ID: store.NewID(), Name: "nas.lan", CertPEM: []byte("c"), KeyPEM: []byte("k"), CA: root.Name},
		{ID: store.NewID(), Name: "other", CertPEM: []byte("c"), KeyPEM: []byte("k"), CA: "Other CA"},
	}
	if err := s.ReplaceCerts(ctx, signed); err != nil {
		t.Fatal(err)
	}
	other := store.Authority{ID: store.NewID(), Name: "Other CA", CertPEM: []byte("cert"), KeyPEM: []byte("key")}
	if err := s.AddAuthority(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameAuthority(ctx, root.Name, other.Name); !errors.Is(err, store.ErrExists) {
		t.Errorf("renaming it to a taken name: %v, want ErrExists", err)
	}
	if err := s.RenameAuthority(ctx, "nope", "Nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("renaming a missing CA: %v, want ErrNotFound", err)
	}
	if err := s.RenameAuthority(ctx, root.Name, "Home CA 2026"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Authorities(ctx)
	if len(got) != 2 || got[0].Name != "Home CA 2026" || got[0].ID != root.ID {
		t.Errorf("Authorities = %+v, want the CA renamed", got)
	}
	certs, _ := s.Certs(ctx)
	if len(certs) != 2 || certs[0].CA != "Home CA 2026" || certs[1].CA != "Other CA" {
		t.Errorf("Certs = %+v, want the certificate signed by the CA to follow it", certs)
	}
	root.Name = "Home CA 2026"

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
