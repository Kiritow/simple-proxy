package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
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

func sites(t *testing.T, s *store.Store) ([]store.Site, int64) {
	t.Helper()
	got, rev, err := s.Sites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return got, rev
}

func TestEmpty(t *testing.T) {
	got, rev := sites(t, open(t, filepath.Join(t.TempDir(), "sites.db")))
	if len(got) != 0 || got == nil || rev != 0 {
		t.Errorf("Sites = %v (nil: %t), %d; want an empty list and revision 0", got, got == nil, rev)
	}
}

func TestReplaceSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	ctx := t.Context()

	a := site("a", `{"server_name": "a.example.com", "default": {"port": 1}}`)
	b := site("b", `{"server_name": "b.example.com"}`)
	rev, err := s.ReplaceSites(ctx, []store.Site{a, b}, 0)
	if err != nil || rev != 1 {
		t.Fatalf("ReplaceSites = %d, %v; want 1", rev, err)
	}

	// Configs come back minified, in order.
	want := []store.Site{
		site("a", `{"server_name":"a.example.com","default":{"port":1}}`),
		site("b", `{"server_name":"b.example.com"}`),
	}
	if got, rev := sites(t, s); !reflect.DeepEqual(got, want) || rev != 1 {
		t.Errorf("Sites = %s, %d; want %s, 1", got, rev, want)
	}

	// Reorder, change b, drop a, add c.
	c := site("c", `{"server_name": "c.example.com"}`)
	b2 := site("b", `{"server_name": "b2.example.com"}`)
	if rev, err := s.ReplaceSites(ctx, []store.Site{c, b2}, 1); err != nil || rev != 2 {
		t.Fatalf("ReplaceSites = %d, %v; want 2", rev, err)
	}
	want = []store.Site{
		site("c", `{"server_name":"c.example.com"}`),
		site("b", `{"server_name":"b2.example.com"}`),
	}
	if got, _ := sites(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("Sites = %s, want %s", got, want)
	}

	// Everything persists.
	s.Close()
	if got, rev := sites(t, open(t, path)); !reflect.DeepEqual(got, want) || rev != 2 {
		t.Errorf("after reopening, Sites = %s, %d; want %s, 2", got, rev, want)
	}
}

func TestReplaceSitesConflict(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	if _, err := s.ReplaceSites(ctx, []store.Site{site("a", `{}`)}, 0); err != nil {
		t.Fatal(err)
	}
	// A second writer that started from revision 0 must not overwrite.
	_, err := s.ReplaceSites(ctx, nil, 0)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("ReplaceSites error = %v, want ErrConflict", err)
	}
	if got, rev := sites(t, s); len(got) != 1 || rev != 1 {
		t.Errorf("after a conflict, Sites = %s, %d; want the earlier site, 1", got, rev)
	}
}

func TestReplaceSitesRejectsBadInput(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	for name, input := range map[string][]store.Site{
		"duplicate ID": {site("a", `{}`), site("a", `{}`)},
		"invalid JSON": {site("a", `{`)},
	} {
		if _, err := s.ReplaceSites(ctx, input, 0); err == nil {
			t.Errorf("%s: ReplaceSites succeeded, want an error", name)
		}
	}
	if got, rev := sites(t, s); len(got) != 0 || rev != 0 {
		t.Errorf("after failed writes, Sites = %s, %d; want nothing stored", got, rev)
	}
}

func TestUpdatedAtChangesOnlyWithConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	ctx := t.Context()
	if _, err := s.ReplaceSites(ctx, []store.Site{site("a", `{}`), site("b", `{}`)}, 0); err != nil {
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
	if _, err := s.ReplaceSites(ctx, []store.Site{site("a", `{ }`), site("b", `{"gzip": true}`)}, 1); err != nil {
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
