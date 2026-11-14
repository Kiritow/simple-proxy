// Package store keeps the sites edited in the UI in an SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Site is a stored site.
type Site struct {
	ID string `json:"id"`
	// Config is the site as one entry of servers in the configuration
	// schema, in JSON.
	Config json.RawMessage `json:"config"`
	// Generation counts the changes of Config, starting at 1, so that
	// changes based on an outdated version of the site can be detected. It
	// is maintained by the Store, and ignored by ReplaceSites.
	Generation int64 `json:"generation"`
}

// Store is a database of sites. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating it if it does not exist.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time anyway, and a single connection
	// serializes transactions without "database is locked" errors.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations[i] upgrades the schema from version i, as recorded in
// PRAGMA user_version, to version i+1. Append new migrations; never change
// the existing ones.
var migrations = [][]string{
	{
		`CREATE TABLE sites (
			id         TEXT PRIMARY KEY,
			position   INTEGER NOT NULL,
			config     TEXT NOT NULL CHECK (json_valid(config)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		// The revision counts the changes to sites, to detect conflicts.
		`CREATE TABLE revision (
			id    INTEGER PRIMARY KEY CHECK (id = 1),
			value INTEGER NOT NULL
		) STRICT`,
		`INSERT INTO revision (id, value) VALUES (1, 0)`,
	},
	{
		// Sites are changed one at a time now, so revisions are not needed.
		`DROP TABLE revision`,
	},
	{
		`ALTER TABLE sites ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`,
	},
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this program supports (%d)", version, len(migrations))
	}
	if version == len(migrations) {
		return nil
	}
	for v := version; v < len(migrations); v++ {
		for _, stmt := range migrations[v] {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migrating the schema to version %d: %w", v+1, err)
			}
		}
	}
	// PRAGMA statements take no parameters.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Sites returns the stored sites in order.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, config, generation FROM sites ORDER BY position")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sites := []Site{}
	for rows.Next() {
		var site Site
		var config string
		if err := rows.Scan(&site.ID, &config, &site.Generation); err != nil {
			return nil, err
		}
		site.Config = json.RawMessage(config)
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

// ReplaceSites stores sites, in this order, in place of the current ones.
// Sites keep their creation time, and their update time and generation
// unless their configuration changed.
func (s *Store) ReplaceSites(ctx context.Context, sites []Site) error {
	ids := make([]string, len(sites))
	seen := make(map[string]bool, len(sites))
	for i, site := range sites {
		if seen[site.ID] {
			return fmt.Errorf("duplicate site ID %q", site.ID)
		}
		seen[site.ID] = true
		ids[i] = site.ID
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for i, site := range sites {
		// json() minifies the config, so that unchanged sites compare equal
		// and keep their updated_at.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sites (id, position, config, created_at, updated_at)
			VALUES (?1, ?2, json(?3), ?4, ?4)
			ON CONFLICT (id) DO UPDATE SET
				position = excluded.position,
				config = excluded.config,
				updated_at = iif(config = excluded.config, updated_at, excluded.updated_at),
				generation = iif(config = excluded.config, generation, generation + 1)`,
			site.ID, i, string(site.Config), now)
		if err != nil {
			return fmt.Errorf("storing site %q: %w", site.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM sites WHERE id NOT IN (SELECT value FROM json_each(?))", string(idList)); err != nil {
		return err
	}
	return tx.Commit()
}
