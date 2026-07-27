// Package store persists the parts of Marina that should outlive a reboot:
// nicknames, pins, and per-service uptime history.
//
// The design rule here is that Postgres is never on the critical path. Live port
// state is always in memory, so the dashboard boots and works at login even if
// Postgres hasn't started yet. The store connects in the background with
// backoff, replays anything written while it was offline, and reports its own
// health so the UI can say so honestly.
package store

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Meta is the user-owned state attached to a service key.
type Meta struct {
	Nickname  string    `json:"nickname,omitempty"`
	Pinned    bool      `json:"pinned"`
	FirstSeen time.Time `json:"firstSeen,omitzero"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
}

// Health describes the store's connection state for display.
type Health struct {
	Connected bool   `json:"connected"`
	DSN       string `json:"dsn"`
	Error     string `json:"error,omitempty"`
	Pending   int    `json:"pending"`
}

// Seen is one observed service, as handed to RecordSeen.
type Seen struct {
	Key       string
	Label     string
	Project   string
	Kind      string
	Port      int
	PID       int
	StartedAt time.Time
}

// Store is a lazily-connected Postgres-backed metadata store with an in-memory
// mirror. All reads are served from memory and never fail.
type Store struct {
	dsn      string
	adminDSN string
	dbName   string

	mu    sync.RWMutex
	meta  map[string]Meta
	dirty map[string]bool

	pool      *pgxpool.Pool
	connected bool
	lastErr   string

	log *slog.Logger
}

const schema = `
CREATE TABLE IF NOT EXISTS services (
  key         text PRIMARY KEY,
  label       text        NOT NULL DEFAULT '',
  project     text        NOT NULL DEFAULT '',
  kind        text        NOT NULL DEFAULT '',
  port        integer     NOT NULL DEFAULT 0,
  nickname    text,
  pinned      boolean     NOT NULL DEFAULT false,
  first_seen  timestamptz NOT NULL DEFAULT now(),
  last_seen   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sightings (
  id          bigserial PRIMARY KEY,
  service_key text        NOT NULL REFERENCES services(key) ON DELETE CASCADE,
  pid         integer     NOT NULL,
  port        integer     NOT NULL,
  started_at  timestamptz NOT NULL,
  ended_at    timestamptz
);

CREATE INDEX IF NOT EXISTS sightings_key_idx ON sightings (service_key, started_at DESC);
-- One open sighting per (service, pid) keeps restart bookkeeping idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS sightings_open_idx
  ON sightings (service_key, pid) WHERE ended_at IS NULL;
`

// New creates the store and starts connecting in the background. It never
// blocks and never returns an error, because a missing database must not stop
// the daemon from serving live state.
func New(ctx context.Context, dsn, adminDSN, dbName string, log *slog.Logger) *Store {
	s := &Store{
		dsn:      dsn,
		adminDSN: adminDSN,
		dbName:   dbName,
		meta:     make(map[string]Meta),
		dirty:    make(map[string]bool),
		log:      log,
	}
	go s.connectLoop(ctx)
	return s
}

// connectLoop retries forever with capped backoff. Once connected it keeps
// verifying the connection so a Postgres restart is noticed and recovered from.
func (s *Store) connectLoop(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if err := s.connect(ctx); err != nil {
			s.setErr(err)
			s.log.Debug("store: connect failed, will retry", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}

		s.log.Info("store: connected", "db", s.dbName)
		backoff = time.Second

		// Stay connected; poll gently so a dropped server is detected.
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.ping(pingCtx)
			cancel()
			if err != nil {
				s.log.Warn("store: connection lost", "err", err)
				s.disconnect(err)
				break
			}
		}
	}
}

func (s *Store) connect(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	pool, err := s.open(dialCtx, s.dsn)
	if err != nil {
		// SQLSTATE 3D000 is invalid_catalog_name: the database isn't there yet.
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "3D000" {
			return err
		}
		if cerr := s.createDatabase(dialCtx); cerr != nil {
			return cerr
		}
		if pool, err = s.open(dialCtx, s.dsn); err != nil {
			return err
		}
	}

	if _, err := pool.Exec(dialCtx, schema); err != nil {
		pool.Close()
		return err
	}

	loaded, err := loadMeta(dialCtx, pool)
	if err != nil {
		pool.Close()
		return err
	}

	s.mu.Lock()
	s.pool = pool
	s.connected = true
	s.lastErr = ""
	// Anything the user changed while offline wins over what's on disk.
	for k, m := range loaded {
		if !s.dirty[k] {
			s.meta[k] = m
		}
	}
	pending := make([]string, 0, len(s.dirty))
	for k := range s.dirty {
		pending = append(pending, k)
	}
	s.mu.Unlock()

	for _, k := range pending {
		if err := s.flush(ctx, k); err != nil {
			s.log.Warn("store: replay failed", "key", k, "err", err)
		}
	}
	return nil
}

func (s *Store) open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// createDatabase connects to the maintenance database to CREATE DATABASE, since
// you cannot create a database from inside a connection to it.
func (s *Store) createDatabase(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, s.adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	// The name comes from our own config, not user input, and CREATE DATABASE
	// does not accept parameters.
	if _, err := conn.Exec(ctx, `CREATE DATABASE "`+strings.ReplaceAll(s.dbName, `"`, `""`)+`"`); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" { // duplicate_database
			return nil
		}
		return err
	}
	s.log.Info("store: created database", "db", s.dbName)
	return nil
}

func loadMeta(ctx context.Context, pool *pgxpool.Pool) (map[string]Meta, error) {
	rows, err := pool.Query(ctx, `SELECT key, COALESCE(nickname,''), pinned, first_seen, last_seen FROM services`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]Meta)
	for rows.Next() {
		var k string
		var m Meta
		if err := rows.Scan(&k, &m.Nickname, &m.Pinned, &m.FirstSeen, &m.LastSeen); err != nil {
			return nil, err
		}
		out[k] = m
	}
	return out, rows.Err()
}

func (s *Store) ping(ctx context.Context) error {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()
	if pool == nil {
		return errors.New("no pool")
	}
	return pool.Ping(ctx)
}

func (s *Store) disconnect(err error) {
	s.mu.Lock()
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
	s.connected = false
	if err != nil {
		s.lastErr = err.Error()
	}
	s.mu.Unlock()
}

func (s *Store) setErr(err error) {
	s.mu.Lock()
	s.connected = false
	s.lastErr = err.Error()
	s.mu.Unlock()
}

// Health reports the current connection state.
func (s *Store) Health() Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dsn := redact(s.dsn)
	return Health{Connected: s.connected, DSN: dsn, Error: s.lastErr, Pending: len(s.dirty)}
}

// Meta returns the stored metadata for a key. Always safe, always in memory.
func (s *Store) Meta(key string) Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta[key]
}

// LastSeenPath reports when any service belonging to the project at path was
// last seen listening, or nil if it never has been.
//
// App keys are built as "app:<repo>|<subpath>|<port>", so a prefix match over the
// in-memory mirror answers this without touching Postgres — which means it still
// works when Postgres is down, and costs nothing on the sweep path.
func (s *Store) LastSeenPath(path string) *time.Time {
	if path == "" {
		return nil
	}
	prefix := "app:" + path + "|"

	s.mu.RLock()
	defer s.mu.RUnlock()

	var latest time.Time
	for key, meta := range s.meta {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if meta.LastSeen.After(latest) {
			latest = meta.LastSeen
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

// PortsForPath reports the ports Marina has actually seen this project listening
// on, most recently first.
//
// This is the strongest port evidence there is, and the only kind that improves on
// its own: it is observation rather than inference. App keys are
// "app:<repo>|<subpath>|<port>", so the ports can be read straight off the
// in-memory mirror — which means it also works while Postgres is down.
func (s *Store) PortsForPath(path string) []int {
	if path == "" {
		return nil
	}
	prefix := "app:" + path + "|"

	s.mu.RLock()
	type seen struct {
		port int
		last time.Time
	}
	var found []seen
	for key, meta := range s.meta {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		// The port is the last |-separated field of the key.
		i := strings.LastIndexByte(key, '|')
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(key[i+1:])
		if err != nil || port <= 0 {
			continue
		}
		found = append(found, seen{port: port, last: meta.LastSeen})
	}
	s.mu.RUnlock()

	sort.Slice(found, func(i, j int) bool { return found[i].last.After(found[j].last) })

	ports := make([]int, 0, len(found))
	dedupe := make(map[int]bool, len(found))
	for _, f := range found {
		if dedupe[f.port] {
			continue
		}
		dedupe[f.port] = true
		ports = append(ports, f.port)
	}
	return ports
}

// SetNickname records a user-chosen display name. The change takes effect in
// memory immediately and is persisted when Postgres is reachable.
func (s *Store) SetNickname(ctx context.Context, key, nickname string) {
	s.update(ctx, key, func(m *Meta) { m.Nickname = strings.TrimSpace(nickname) })
}

// SetPinned pins or unpins a service.
func (s *Store) SetPinned(ctx context.Context, key string, pinned bool) {
	s.update(ctx, key, func(m *Meta) { m.Pinned = pinned })
}

func (s *Store) update(ctx context.Context, key string, mutate func(*Meta)) {
	s.mu.Lock()
	m := s.meta[key]
	mutate(&m)
	if m.FirstSeen.IsZero() {
		m.FirstSeen = time.Now()
	}
	m.LastSeen = time.Now()
	s.meta[key] = m
	s.dirty[key] = true
	connected := s.connected
	s.mu.Unlock()

	if connected {
		if err := s.flush(ctx, key); err != nil {
			s.log.Warn("store: persist failed, keeping in memory", "key", key, "err", err)
		}
	}
}

// flush writes one key's metadata and clears its dirty flag on success.
func (s *Store) flush(ctx context.Context, key string) error {
	s.mu.RLock()
	pool, m := s.pool, s.meta[key]
	s.mu.RUnlock()
	if pool == nil {
		return errors.New("store: not connected")
	}

	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var nickname *string
	if m.Nickname != "" {
		nickname = &m.Nickname
	}
	_, err := pool.Exec(writeCtx, `
		INSERT INTO services (key, nickname, pinned, first_seen, last_seen)
		VALUES ($1, $2, $3, COALESCE($4, now()), now())
		ON CONFLICT (key) DO UPDATE
		  SET nickname = EXCLUDED.nickname,
		      pinned    = EXCLUDED.pinned,
		      last_seen = now()`,
		key, nickname, m.Pinned, nullTime(m.FirstSeen))
	if err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.dirty, key)
	s.mu.Unlock()
	return nil
}

// RecordSeen upserts the currently-live services and maintains uptime history:
// it opens a sighting for anything newly seen and closes sightings for PIDs that
// are no longer listening. Errors are logged rather than propagated, since
// history is a nicety and must not disturb the live view.
func (s *Store) RecordSeen(ctx context.Context, seen []Seen) {
	s.mu.RLock()
	pool, connected := s.pool, s.connected
	s.mu.RUnlock()
	if !connected || pool == nil {
		return
	}

	// Refresh the in-memory first/last seen so the UI shows history even before
	// the round trip completes.
	now := time.Now()
	s.mu.Lock()
	for _, sv := range seen {
		m := s.meta[sv.Key]
		if m.FirstSeen.IsZero() {
			m.FirstSeen = now
		}
		m.LastSeen = now
		s.meta[sv.Key] = m
	}
	s.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	batch := &pgx.Batch{}
	keys := make([]string, 0, len(seen))
	for _, sv := range seen {
		keys = append(keys, sv.Key)
		batch.Queue(`
			INSERT INTO services (key, label, project, kind, port, first_seen, last_seen)
			VALUES ($1, $2, $3, $4, $5, now(), now())
			ON CONFLICT (key) DO UPDATE
			  SET label = EXCLUDED.label,
			      project = EXCLUDED.project,
			      kind = EXCLUDED.kind,
			      port = EXCLUDED.port,
			      last_seen = now()`,
			sv.Key, sv.Label, sv.Project, sv.Kind, sv.Port)

		started := sv.StartedAt
		if started.IsZero() {
			started = now
		}
		batch.Queue(`
			INSERT INTO sightings (service_key, pid, port, started_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (service_key, pid) WHERE ended_at IS NULL DO NOTHING`,
			sv.Key, sv.PID, sv.Port, started)
	}

	// Close out anything whose key is no longer live.
	batch.Queue(`UPDATE sightings SET ended_at = now()
	             WHERE ended_at IS NULL AND service_key <> ALL($1)`, keys)

	if err := pool.SendBatch(writeCtx, batch).Close(); err != nil {
		s.log.Debug("store: RecordSeen batch failed", "err", err)
	}
}

// History returns aggregate uptime facts for a service key.
func (s *Store) History(ctx context.Context, key string) (map[string]any, error) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()
	if pool == nil {
		return nil, errors.New("store: not connected")
	}

	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var starts int64
	var totalSecs float64
	var firstSeen, lastSeen *time.Time
	err := pool.QueryRow(qCtx, `
		SELECT count(*),
		       COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, now()) - started_at))), 0),
		       MIN(started_at),
		       MAX(COALESCE(ended_at, now()))
		FROM sightings WHERE service_key = $1`, key).
		Scan(&starts, &totalSecs, &firstSeen, &lastSeen)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"key":          key,
		"starts":       starts,
		"totalSeconds": int64(totalSecs),
		"firstSeen":    firstSeen,
		"lastSeen":     lastSeen,
	}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.disconnect(nil)
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// redact strips any password from a DSN before it is shown in the UI.
func redact(dsn string) string {
	at := strings.LastIndexByte(dsn, '@')
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return dsn
	}
	creds := dsn[scheme+3 : at]
	if i := strings.IndexByte(creds, ':'); i >= 0 {
		return dsn[:scheme+3] + creds[:i] + ":***" + dsn[at:]
	}
	return dsn
}
