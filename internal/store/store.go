// Package store keeps the sites edited in the UI, the certificates and
// certificate authorities uploaded to it, and the users of the console with
// their sessions, in an SQLite database.
package store

import (
	"context"
	"crypto/rand"
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
	// CreatedAt is when the site was added, and UpdatedAt when its Config
	// last changed. Like Generation, they are maintained by the Store.
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Cert is a certificate kept by simple-proxy, as opposed to acme.sh.
type Cert struct {
	// ID identifies the certificate for good, see NewID, while its Name may
	// change.
	ID string
	// Name is unique too; sites refer to the certificate by name.
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
	ID              string // like Cert's
	Name            string // unique; certificates refer to their CA by name
	CertPEM, KeyPEM []byte
	CreatedAt       time.Time // maintained by the Store
}

var (
	// ErrExists reports that a CA of the same name exists.
	ErrExists = errors.New("already exists")
	// ErrNotFound reports that a CA does not exist.
	ErrNotFound = errors.New("not found")
)

// NewID returns a new ID for a certificate or a CA: a random UUID (version
// 4), such as "0b5c2fd4-5d6e-4b8a-9f3e-3c1d2a7b9e10".
func NewID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// sqlNewID is NewID in SQL, for the rows that predate IDs.
const sqlNewID = `lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
	substr('89ab', 1 + (random() & 3), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))`

// Store is a database of sites. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating it if it does not exist. It
// holds private keys, so only its owner may read it.
func Open(ctx context.Context, path string) (*Store, error) {
	// Foreign keys remove the assignments of deleted sites and users, and the
	// sessions of deleted users.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
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
	{
		// Certificates and CAs get IDs, so that they can be renamed.
		`CREATE TABLE certificates_v5 (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			cert       TEXT NOT NULL,
			key        TEXT NOT NULL,
			ca         TEXT NOT NULL,
			key_type   TEXT NOT NULL,
			days       INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		`INSERT INTO certificates_v5
			SELECT ` + sqlNewID + `, name, cert, key, ca, key_type, days, created_at, updated_at
			FROM certificates`,
		`DROP TABLE certificates`,
		`ALTER TABLE certificates_v5 RENAME TO certificates`,
		`CREATE TABLE authorities_v5 (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			cert       TEXT NOT NULL,
			key        TEXT NOT NULL,
			created_at TEXT NOT NULL
		) STRICT`,
		`INSERT INTO authorities_v5
			SELECT ` + sqlNewID + `, name, cert, key, created_at FROM authorities`,
		`DROP TABLE authorities`,
		`ALTER TABLE authorities_v5 RENAME TO authorities`,
	},
	{
		// Users sign in with a password, an OIDC provider, or both.
		`CREATE TABLE users (
			id             TEXT PRIMARY KEY,
			username       TEXT NOT NULL UNIQUE COLLATE NOCASE,
			display_name   TEXT NOT NULL,
			email          TEXT NOT NULL,
			email_verified INTEGER NOT NULL,
			role           TEXT NOT NULL CHECK (role IN ('', 'site_operator', 'platform_operator', 'admin')),
			password_hash  TEXT NOT NULL,
			oidc_issuer    TEXT NOT NULL,
			oidc_subject   TEXT NOT NULL,
			managed        INTEGER NOT NULL,
			disabled       INTEGER NOT NULL,
			created_at     TEXT NOT NULL,
			updated_at     TEXT NOT NULL,
			last_login_at  TEXT NOT NULL
		) STRICT`,
		`CREATE UNIQUE INDEX users_oidc ON users (oidc_issuer, oidc_subject) WHERE oidc_subject != ''`,
		// The sites that site operators manage.
		`CREATE TABLE user_sites (
			user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
			site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, site_id)
		) STRICT, WITHOUT ROWID`,
		`CREATE INDEX user_sites_site ON user_sites (site_id)`,
		`CREATE TABLE sessions (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
			method     TEXT NOT NULL,
			created_at TEXT NOT NULL,
			seen_at    TEXT NOT NULL,
			expires_at TEXT NOT NULL
		) STRICT`,
		`CREATE INDEX sessions_user ON sessions (user_id)`,
		`CREATE TABLE settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		) STRICT`,
	},
	{
		// Every change of a site is kept as a version. Versions outlive their
		// sites, so they do not refer to them by a foreign key.
		`CREATE TABLE site_versions (
			id         INTEGER PRIMARY KEY,
			site_id    TEXT NOT NULL,
			generation INTEGER NOT NULL,
			kind       TEXT NOT NULL CHECK (kind IN ('initial', 'create', 'update', 'delete')),
			config     TEXT NOT NULL,
			author     TEXT NOT NULL,
			created_at TEXT NOT NULL
		) STRICT`,
		`CREATE INDEX site_versions_site ON site_versions (site_id, id)`,
		`CREATE INDEX site_versions_created ON site_versions (created_at)`,
		// The sites as they are when versions start being kept.
		`INSERT INTO site_versions (site_id, generation, kind, config, author, created_at)
			SELECT id, generation, 'initial', config, '', updated_at FROM sites ORDER BY position`,
	},
	{
		// Personal API tokens, for scripts and MCP clients acting as users.
		`CREATE TABLE api_tokens (
			id           TEXT PRIMARY KEY,
			hash         TEXT NOT NULL UNIQUE,
			user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
			name         TEXT NOT NULL,
			prefix       TEXT NOT NULL,
			created_at   TEXT NOT NULL,
			last_used_at TEXT NOT NULL,
			expires_at   TEXT NOT NULL
		) STRICT`,
		`CREATE INDEX api_tokens_user ON api_tokens (user_id)`,
	},
}

// migrate upgrades the schema. Foreign keys are off meanwhile, as SQLite
// recommends, so that rebuilding a table does not delete the rows referring
// to it, e.g. the sites of users; they are checked before committing.
func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// The pragma has no effect within transactions.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
	tx, err := conn.BeginTx(ctx, nil)
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
	var violations int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		return err
	}
	if violations > 0 {
		return fmt.Errorf("migrating the schema left %d rows referring to missing ones", violations)
	}
	return tx.Commit()
}

// Sites returns the stored sites in order.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, config, generation, created_at, updated_at FROM sites ORDER BY position")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sites := []Site{}
	for rows.Next() {
		var site Site
		var config, created, updated string
		if err := rows.Scan(&site.ID, &config, &site.Generation, &created, &updated); err != nil {
			return nil, err
		}
		site.Config = json.RawMessage(config)
		site.CreatedAt, _ = time.Parse(time.RFC3339, created)
		site.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
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

	// The generations before the change tell which sites are new or
	// changed, to keep a version of them.
	before := map[string]int64{}
	rows, err := tx.QueryContext(ctx, "SELECT id, generation FROM sites")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var generation int64
		if err := rows.Scan(&id, &generation); err != nil {
			rows.Close()
			return err
		}
		before[id] = generation
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	author := authorOf(ctx)
	for i, site := range sites {
		// json() minifies the config, so that unchanged sites compare equal
		// and keep their updated_at.
		var generation int64
		var config string
		err := tx.QueryRowContext(ctx, `
			INSERT INTO sites (id, position, config, created_at, updated_at)
			VALUES (?1, ?2, json(?3), ?4, ?4)
			ON CONFLICT (id) DO UPDATE SET
				position = excluded.position,
				config = excluded.config,
				updated_at = iif(config = excluded.config, updated_at, excluded.updated_at),
				generation = iif(config = excluded.config, generation, generation + 1)
			RETURNING generation, config`,
			site.ID, i, string(site.Config), now).Scan(&generation, &config)
		if err != nil {
			return fmt.Errorf("storing site %q: %w", site.ID, err)
		}
		previous, existed := before[site.ID]
		switch {
		case !existed:
			err = addVersion(ctx, tx, site.ID, generation, VersionCreate, config, author, now)
		case generation != previous:
			err = addVersion(ctx, tx, site.ID, generation, VersionUpdate, config, author, now)
		}
		if err != nil {
			return err
		}
	}
	// Deleted sites get a version too, with the configuration they had.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO site_versions (site_id, generation, kind, config, author, created_at)
		SELECT id, generation, ?, config, ?, ? FROM sites
		WHERE id NOT IN (SELECT value FROM json_each(?)) ORDER BY position`,
		string(VersionDelete), author, now, string(idList))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		"DELETE FROM sites WHERE id NOT IN (SELECT value FROM json_each(?))", string(idList))
	return err
}

// Certs returns the stored certificates, by name.
func (s *Store) Certs(ctx context.Context) ([]Cert, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cert, key, ca, key_type, days, created_at, updated_at
		FROM certificates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	certs := []Cert{}
	for rows.Next() {
		var c Cert
		var cert, key, created, updated string
		if err := rows.Scan(&c.ID, &c.Name, &cert, &key, &c.CA, &c.KeyType, &c.Days, &created, &updated); err != nil {
			return nil, err
		}
		c.CertPEM, c.KeyPEM = []byte(cert), []byte(key)
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

// ReplaceCerts stores certs in place of the current certificates, which
// they replace by ID: a certificate keeps its creation time, and its update
// time unless its contents changed, also when it is renamed.
func (s *Store) ReplaceCerts(ctx context.Context, certs []Cert) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return replaceCerts(ctx, tx, certs) })
}

func replaceCerts(ctx context.Context, tx *sql.Tx, certs []Cert) error {
	ids := make([]string, len(certs))
	names := make([]string, len(certs))
	for i, c := range certs {
		switch {
		case c.ID == "":
			return fmt.Errorf("certificate %q has no ID", c.Name)
		case slices.Contains(ids[:i], c.ID):
			return fmt.Errorf("duplicate certificate ID %q", c.ID)
		case slices.Contains(names[:i], c.Name):
			return fmt.Errorf("duplicate certificate %q", c.Name)
		}
		ids[i], names[i] = c.ID, c.Name
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	// Deleting first frees the names of deleted certificates for the others.
	_, err = tx.ExecContext(ctx,
		"DELETE FROM certificates WHERE id NOT IN (SELECT value FROM json_each(?))", string(idList))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range certs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO certificates (id, name, cert, key, ca, key_type, days, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?8)
			ON CONFLICT (id) DO UPDATE SET
				updated_at = iif(cert = excluded.cert AND key = excluded.key, updated_at, excluded.updated_at),
				name = excluded.name,
				cert = excluded.cert,
				key = excluded.key,
				ca = excluded.ca,
				key_type = excluded.key_type,
				days = excluded.days`,
			c.ID, c.Name, string(c.CertPEM), string(c.KeyPEM), c.CA, c.KeyType, c.Days, now)
		if err != nil {
			return fmt.Errorf("storing certificate %q: %w", c.Name, err)
		}
	}
	return nil
}

// Authorities returns the stored CAs, by name.
func (s *Store) Authorities(ctx context.Context) ([]Authority, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, cert, key, created_at FROM authorities ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cas := []Authority{}
	for rows.Next() {
		var a Authority
		var cert, key, created string
		if err := rows.Scan(&a.ID, &a.Name, &cert, &key, &created); err != nil {
			return nil, err
		}
		a.CertPEM, a.KeyPEM = []byte(cert), []byte(key)
		a.CreatedAt, _ = time.Parse(time.RFC3339, created)
		cas = append(cas, a)
	}
	return cas, rows.Err()
}

// AddAuthority stores a new CA, which needs an ID from NewID. If one of the
// same name exists, it fails with ErrExists.
func (s *Store) AddAuthority(ctx context.Context, a Authority) error {
	if a.ID == "" {
		return fmt.Errorf("CA %q has no ID", a.Name)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO authorities (id, name, cert, key, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		a.ID, a.Name, string(a.CertPEM), string(a.KeyPEM), time.Now().UTC().Format(time.RFC3339))
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

// RenameAuthority renames a CA, and changes the certificates it signed to
// refer to it by its new name. If there is no CA of that name, it fails with
// ErrNotFound, and if another CA has the new name, with ErrExists.
func (s *Store) RenameAuthority(ctx context.Context, name, newName string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var taken bool
		err := tx.QueryRowContext(ctx, "SELECT count(*) > 0 FROM authorities WHERE name = ?", newName).Scan(&taken)
		if err != nil {
			return err
		}
		if taken && newName != name {
			return fmt.Errorf("CA %q: %w", newName, ErrExists)
		}
		res, err := tx.ExecContext(ctx, "UPDATE authorities SET name = ? WHERE name = ?", newName, name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("CA %q: %w", name, ErrNotFound)
		}
		_, err = tx.ExecContext(ctx, "UPDATE certificates SET ca = ? WHERE ca = ?", newName, name)
		return err
	})
}
