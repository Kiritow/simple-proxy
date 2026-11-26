// Package store keeps the sites edited in the UI, and the certificates and
// certificate authorities uploaded to it, in an SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
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

// Cert is a certificate kept by simple-proxy, as opposed to acme.sh.
type Cert struct {
	// Name identifies the certificate; sites refer to it by name.
	Name string
	// CertPEM is the certificate chain, and KeyPEM its private key.
	CertPEM, KeyPEM []byte
	// CA names the CA that signed the certificate, which signs it again on
	// renewal with the same KeyType and Days. It is empty for uploaded
	// certificates.
	CA      string
	KeyType string
	Days    int
	// CreatedAt and UpdatedAt are maintained by the Store, like Site's
	// Generation.
	CreatedAt, UpdatedAt time.Time
}

// Authority is a certificate authority (CA) that signs certificates.
type Authority struct {
	Name            string
	CertPEM, KeyPEM []byte
	CreatedAt       time.Time // maintained by the Store
}

var (
	// ErrExists reports that a CA of the same name exists.
	ErrExists = errors.New("already exists")
	// ErrNotFound reports that a CA does not exist.
	ErrNotFound = errors.New("not found")
)

// Store is a database of sites. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating it if it does not exist. It
// holds private keys, so only its owner may read it.
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
	// SQLite creates its journal with the permissions of the database.
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		db.Close()
		return nil, err
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
	{
		`CREATE TABLE certificates (
			name       TEXT PRIMARY KEY,
			cert       TEXT NOT NULL,
			key        TEXT NOT NULL,
			ca         TEXT NOT NULL,
			key_type   TEXT NOT NULL,
			days       INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE authorities (
			name       TEXT PRIMARY KEY,
			cert       TEXT NOT NULL,
			key        TEXT NOT NULL,
			created_at TEXT NOT NULL
		) STRICT`,
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
	return s.inTx(ctx, func(tx *sql.Tx) error { return replaceSites(ctx, tx, sites) })
}

// Replace replaces both the sites, like ReplaceSites, and the
// certificates, like ReplaceCerts, at once.
func (s *Store) Replace(ctx context.Context, sites []Site, certs []Cert) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := replaceSites(ctx, tx, sites); err != nil {
			return err
		}
		return replaceCerts(ctx, tx, certs)
	})
}

func (s *Store) inTx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceSites(ctx context.Context, tx *sql.Tx, sites []Site) error {
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
	_, err = tx.ExecContext(ctx,
		"DELETE FROM sites WHERE id NOT IN (SELECT value FROM json_each(?))", string(idList))
	return err
}

// Certs returns the stored certificates, by name.
func (s *Store) Certs(ctx context.Context) ([]Cert, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, cert, key, ca, key_type, days, created_at, updated_at
		FROM certificates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	certs := []Cert{}
	for rows.Next() {
		var c Cert
		var cert, key, created, updated string
		if err := rows.Scan(&c.Name, &cert, &key, &c.CA, &c.KeyType, &c.Days, &created, &updated); err != nil {
			return nil, err
		}
		c.CertPEM, c.KeyPEM = []byte(cert), []byte(key)
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

// ReplaceCerts stores certs in place of the current certificates.
// Certificates keep their creation time, and their update time unless their
// contents changed.
func (s *Store) ReplaceCerts(ctx context.Context, certs []Cert) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return replaceCerts(ctx, tx, certs) })
}

func replaceCerts(ctx context.Context, tx *sql.Tx, certs []Cert) error {
	names := make([]string, len(certs))
	for i, c := range certs {
		if slices.Contains(names[:i], c.Name) {
			return fmt.Errorf("duplicate certificate %q", c.Name)
		}
		names[i] = c.Name
	}
	nameList, err := json.Marshal(names)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range certs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO certificates (name, cert, key, ca, key_type, days, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)
			ON CONFLICT (name) DO UPDATE SET
				updated_at = iif(cert = excluded.cert AND key = excluded.key, updated_at, excluded.updated_at),
				cert = excluded.cert,
				key = excluded.key,
				ca = excluded.ca,
				key_type = excluded.key_type,
				days = excluded.days`,
			c.Name, string(c.CertPEM), string(c.KeyPEM), c.CA, c.KeyType, c.Days, now)
		if err != nil {
			return fmt.Errorf("storing certificate %q: %w", c.Name, err)
		}
	}
	_, err = tx.ExecContext(ctx,
		"DELETE FROM certificates WHERE name NOT IN (SELECT value FROM json_each(?))", string(nameList))
	return err
}

// Authorities returns the stored CAs, by name.
func (s *Store) Authorities(ctx context.Context) ([]Authority, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, cert, key, created_at FROM authorities ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cas := []Authority{}
	for rows.Next() {
		var a Authority
		var cert, key, created string
		if err := rows.Scan(&a.Name, &cert, &key, &created); err != nil {
			return nil, err
		}
		a.CertPEM, a.KeyPEM = []byte(cert), []byte(key)
		a.CreatedAt, _ = time.Parse(time.RFC3339, created)
		cas = append(cas, a)
	}
	return cas, rows.Err()
}

// AddAuthority stores a new CA. If one of the same name exists, it fails
// with ErrExists.
func (s *Store) AddAuthority(ctx context.Context, a Authority) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO authorities (name, cert, key, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		a.Name, string(a.CertPEM), string(a.KeyPEM), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("CA %q: %w", a.Name, ErrExists)
	}
	return nil
}

// DeleteAuthority removes a CA. If there is none of that name, it fails
// with ErrNotFound.
func (s *Store) DeleteAuthority(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM authorities WHERE name = ?", name)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("CA %q: %w", name, ErrNotFound)
	}
	return nil
}
