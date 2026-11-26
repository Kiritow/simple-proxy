package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// VersionKind tells what made a version of a site.
type VersionKind string

const (
	// VersionInitial is the site as it was when versions started being
	// kept.
	VersionInitial VersionKind = "initial"
	VersionCreate  VersionKind = "create"
	VersionUpdate  VersionKind = "update"
	// VersionDelete keeps the configuration the site had when deleted.
	VersionDelete VersionKind = "delete"
)

// Version is a version of a site: its configuration after a change, which
// the Store keeps whenever a site is added, changed or deleted.
type Version struct {
	ID         int64 // increases with every version
	SiteID     string
	Generation int64 // of the site, see Site.Generation
	Kind       VersionKind
	Config     json.RawMessage
	Author     string // the username of who made the change, if known
	CreatedAt  time.Time
}

type authorKey struct{}

// WithAuthor returns ctx telling that changes made with it are made by the
// user with the given username, which the versions of sites record.
func WithAuthor(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, authorKey{}, username)
}

func authorOf(ctx context.Context) string {
	author, _ := ctx.Value(authorKey{}).(string)
	return author
}

func addVersion(ctx context.Context, tx *sql.Tx, siteID string, generation int64, kind VersionKind, config, author, at string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO site_versions (site_id, generation, kind, config, author, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		siteID, generation, string(kind), config, author, at)
	if err != nil {
		return fmt.Errorf("keeping a version of site %q: %w", siteID, err)
	}
	return nil
}

// SiteVersions returns the versions of the site with the given ID, oldest
// first.
func (s *Store) SiteVersions(ctx context.Context, siteID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, site_id, generation, kind, config, author, created_at
		FROM site_versions WHERE site_id = ? ORDER BY id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []Version{}
	for rows.Next() {
		var v Version
		var kind, config, created string
		if err := rows.Scan(&v.ID, &v.SiteID, &v.Generation, &kind, &config, &v.Author, &created); err != nil {
			return nil, err
		}
		v.Kind, v.Config, v.CreatedAt = VersionKind(kind), json.RawMessage(config), parseTime(created)
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// currentVersions selects the IDs of the latest versions of the sites that
// exist, which hold their current configuration.
const currentVersions = `SELECT max(v.id) FROM site_versions v JOIN sites s ON s.id = v.site_id GROUP BY v.site_id`

// VersionStats sums up the versions, and the versions of each day.
type VersionStats struct {
	Versions int64 `json:"versions"`
	// Purgeable are the versions that PurgeVersions may delete: all but the
	// latest version of each site that exists.
	Purgeable int64 `json:"purgeable"`
	Bytes     int64 `json:"bytes"` // of their configurations
	Sites     int64 `json:"sites"` // that they are versions of, deleted ones included
	// Oldest and Newest are when the oldest and the newest versions were
	// made; zero without versions.
	Oldest time.Time    `json:"oldest,omitzero"`
	Newest time.Time    `json:"newest,omitzero"`
	Days   []VersionDay `json:"days"` // with versions, oldest first
}

// VersionDay sums up the versions made on a day.
type VersionDay struct {
	Date      string `json:"date"` // such as "2026-11-27"
	Versions  int64  `json:"versions"`
	Purgeable int64  `json:"purgeable"`
	Bytes     int64  `json:"bytes"`
	// The versions by kind.
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
	Deleted int64 `json:"deleted"`
	Initial int64 `json:"initial"`
}

// VersionStats sums up the versions by the day they were made, in the time
// zone that is offset east of UTC.
func (s *Store) VersionStats(ctx context.Context, offset time.Duration) (VersionStats, error) {
	var stats VersionStats
	var oldest, newest sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(id NOT IN (`+currentVersions+`)), 0), coalesce(sum(length(config)), 0),
			count(DISTINCT site_id), min(created_at), max(created_at)
		FROM site_versions`).Scan(&stats.Versions, &stats.Purgeable, &stats.Bytes, &stats.Sites, &oldest, &newest)
	if err != nil {
		return VersionStats{}, err
	}
	stats.Oldest, stats.Newest = parseTime(oldest.String), parseTime(newest.String)

	// The modifier of SQLite's date shifting UTC to the time zone.
	shift := fmt.Sprintf("%+d minutes", int64(offset/time.Minute))
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(created_at, ?1) AS day, count(*), sum(id NOT IN (`+currentVersions+`)), sum(length(config)),
			sum(kind = 'create'), sum(kind = 'update'), sum(kind = 'delete'), sum(kind = 'initial')
		FROM site_versions GROUP BY day ORDER BY day`, shift)
	if err != nil {
		return VersionStats{}, err
	}
	defer rows.Close()
	stats.Days = []VersionDay{}
	for rows.Next() {
		var d VersionDay
		if err := rows.Scan(&d.Date, &d.Versions, &d.Purgeable, &d.Bytes, &d.Created, &d.Updated, &d.Deleted, &d.Initial); err != nil {
			return VersionStats{}, err
		}
		stats.Days = append(stats.Days, d)
	}
	return stats, rows.Err()
}

// PurgeVersions deletes the versions made before the given time, but the
// latest version of each site that exists, and returns how many it
// deleted.
func (s *Store) PurgeVersions(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM site_versions WHERE created_at < ? AND id NOT IN (`+currentVersions+`)`,
		formatTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
