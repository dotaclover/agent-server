package persistence

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func OpenSQLite(path string) (*sql.DB, error) {
	if path == "" {
		path = "data/agent_studio.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

type SQLiteStatus struct {
	OK                 bool   `json:"ok"`
	JournalMode        string `json:"journal_mode,omitempty"`
	BusyTimeoutMS      int    `json:"busy_timeout_ms,omitempty"`
	ForeignKeys        bool   `json:"foreign_keys"`
	MaxOpenConnections int    `json:"max_open_connections"`
	OpenConnections    int    `json:"open_connections"`
	InUseConnections   int    `json:"in_use_connections"`
	IdleConnections    int    `json:"idle_connections"`
	WaitCount          int64  `json:"wait_count"`
	WaitDurationMS     int64  `json:"wait_duration_ms"`
	MaxIdleClosed      int64  `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64  `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64  `json:"max_lifetime_closed"`
	Error              string `json:"error,omitempty"`
}

func InspectSQLite(db *sql.DB) SQLiteStatus {
	if db == nil {
		return SQLiteStatus{OK: false, Error: "database is not configured"}
	}
	stats := db.Stats()
	status := SQLiteStatus{
		OK:                 true,
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUseConnections:   stats.InUse,
		IdleConnections:    stats.Idle,
		WaitCount:          stats.WaitCount,
		WaitDurationMS:     stats.WaitDuration.Milliseconds(),
		MaxIdleClosed:      stats.MaxIdleClosed,
		MaxIdleTimeClosed:  stats.MaxIdleTimeClosed,
		MaxLifetimeClosed:  stats.MaxLifetimeClosed,
	}
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&status.JournalMode); err != nil {
		status.OK = false
		status.Error = err.Error()
		return status
	}
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&status.BusyTimeoutMS); err != nil {
		status.OK = false
		status.Error = err.Error()
		return status
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		status.OK = false
		status.Error = err.Error()
		return status
	}
	status.ForeignKeys = foreignKeys == 1
	return status
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	role TEXT NOT NULL DEFAULT '',
	messages_json TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	facts_json TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS trace_events (
	id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	type TEXT NOT NULL,
	message TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	payload_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	PRIMARY KEY (session_id, id)
);

CREATE INDEX IF NOT EXISTS idx_trace_events_session_created ON trace_events(session_id, created_at);
`)
	if err != nil {
		return err
	}
	if err := ensureSessionRoleColumn(db); err != nil {
		return err
	}
	return ensureSessionIndexes(db)
}

func ensureSessionRoleColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "role" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT ''`)
	return err
}

func ensureSessionIndexes(db *sql.DB) error {
	_, err := db.Exec(`
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_role_updated ON sessions(role, updated_at DESC);
`)
	return err
}
