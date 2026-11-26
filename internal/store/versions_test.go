package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/store"
)

// kinds returns the kinds, generations, configs and authors of versions.
func kinds(versions []store.Version) []string {
	var got []string
	for _, v := range versions {
		got = append(got, string(v.Kind)+" "+string(rune('0'+v.Generation))+" "+string(v.Config)+" "+v.Author)
	}
	return got
}

func TestVersions(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := store.WithAuthor(t.Context(), "alice")

	steps := [][]store.Site{
		{site("a", `{"x": 1}`), site("b", `{"y": 1}`)},
		{site("a", `{"x": 2}`), site("b", `{"y": 1}`)}, // a changes
		{site("b", `{"y": 1}`), site("a", `{"x": 2}`)}, // reordered: no change
		{site("b", `{"y": 1}`)},                        // a is deleted
	}
	for _, sites := range steps {
		if err := s.ReplaceSites(ctx, sites); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.SiteVersions(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`create 1 {"x":1} alice`, `update 2 {"x":2} alice`, `delete 2 {"x":2} alice`}
	if got := kinds(a); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("versions of a = %q, want %q", got, want)
	}
	if a[0].CreatedAt.IsZero() || a[0].ID >= a[1].ID {
		t.Errorf("versions of a = %+v", a)
	}
	// Changes without an author, e.g. from scripts of earlier versions.
	if err := s.ReplaceSites(t.Context(), []store.Site{site("b", `{"y": 2}`)}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.SiteVersions(ctx, "b")
	if got := kinds(b); len(got) != 2 || got[1] != `update 2 {"y":2} ` {
		t.Errorf("versions of b = %q", got)
	}

	// Certificates do not make versions of sites.
	if err := s.Replace(ctx, []store.Site{site("b", `{"y": 2}`)}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.SiteVersions(ctx, "b"); len(b) != 2 {
		t.Errorf("an unchanged site got a version: %q", kinds(b))
	}
}

func TestVersionStatsAndPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	ctx := t.Context()
	for _, sites := range [][]store.Site{
		{site("a", `{"x": 1}`), site("b", `{"y": 1}`)},
		{site("a", `{"x": 2}`), site("b", `{"y": 1}`)},
		{site("a", `{"x": 3}`)},
	} {
		if err := s.ReplaceSites(ctx, sites); err != nil {
			t.Fatal(err)
		}
	}
	// Back-date the first changes to the evening of 2026-01-01 in UTC,
	// which is 2026-01-02 at UTC+8, and the deletion to 2026-02-01.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`UPDATE site_versions SET created_at = '2026-01-01T20:00:00Z' WHERE kind != 'delete' AND config != '{"x":3}'`,
		`UPDATE site_versions SET created_at = '2026-02-01T10:00:00Z' WHERE kind = 'delete'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.VersionStats(ctx, 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// a: create, update, update (current); b: create, delete.
	if stats.Versions != 5 || stats.Purgeable != 4 || stats.Sites != 2 || stats.Bytes == 0 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.Oldest.Format(time.RFC3339) != "2026-01-01T20:00:00Z" || stats.Newest.Before(stats.Oldest) {
		t.Errorf("oldest %v, newest %v", stats.Oldest, stats.Newest)
	}
	if len(stats.Days) != 3 {
		t.Fatalf("days = %+v, want 3", stats.Days)
	}
	if d := stats.Days[0]; d.Date != "2026-01-02" || d.Versions != 3 || d.Created != 2 || d.Updated != 1 || d.Purgeable != 3 {
		t.Errorf("first day = %+v", d)
	}
	if d := stats.Days[1]; d.Date != "2026-02-01" || d.Deleted != 1 || d.Purgeable != 1 {
		t.Errorf("second day = %+v", d)
	}
	if d := stats.Days[2]; d.Versions != 1 || d.Updated != 1 || d.Purgeable != 0 {
		t.Errorf("last day, of a's current version = %+v", d)
	}
	if utc, _ := s.VersionStats(ctx, 0); utc.Days[0].Date != "2026-01-01" {
		t.Errorf("first day in UTC = %+v", utc.Days[0])
	}

	// Purging keeps the latest version of each site that exists.
	n, err := s.PurgeVersions(ctx, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 3 {
		t.Errorf("PurgeVersions before 2026-01-15 = %d, %v; want 3", n, err)
	}
	n, err = s.PurgeVersions(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Errorf("PurgeVersions of all = %d, %v; want 1 (b's deletion)", n, err)
	}
	if a, _ := s.SiteVersions(ctx, "a"); len(a) != 1 || string(a[0].Config) != `{"x":3}` {
		t.Errorf("versions of a after purging = %q, want its current one", kinds(a))
	}
	if stats, _ := s.VersionStats(ctx, 0); stats.Versions != 1 || stats.Purgeable != 0 {
		t.Errorf("stats after purging = %+v", stats)
	}
}

// Sites stored before versions were kept get an initial version.
func TestMigrationAddsInitialVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.db")
	s := open(t, path)
	if err := s.ReplaceSites(t.Context(), []store.Site{site("a", `{"x": 1}`), site("b", `{"y": 1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSites(t.Context(), []store.Site{site("a", `{"x": 2}`), site("b", `{"y": 1}`)}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Go back to the schema without versions.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`DROP TABLE site_versions`, `DROP TABLE api_tokens`, `PRAGMA user_version = 6`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s = open(t, path)
	a, err := s.SiteVersions(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(a); len(got) != 1 || got[0] != `initial 2 {"x":2} ` || a[0].CreatedAt.IsZero() {
		t.Errorf("versions of a = %q", got)
	}
}
