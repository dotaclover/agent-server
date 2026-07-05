package trace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"go-agent-studio/services/security"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxQueryLimit      = 200
	MaxQueryFilterSize = 64
	MaxQuerySinceHours = 24 * 365 * 10
)

type Event struct {
	ID         string                 `json:"id"`
	SessionID  string                 `json:"session_id"`
	Type       string                 `json:"type"`
	Message    string                 `json:"message"`
	ToolName   string                 `json:"tool_name,omitempty"`
	Status     string                 `json:"status,omitempty"`
	DurationMS int64                  `json:"duration_ms,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

type QueryOptions struct {
	Limit      int
	Type       string
	Status     string
	Since      time.Time
	SinceHours int
}

type QueryResult struct {
	Events         []Event           `json:"events"`
	TotalEvents    int               `json:"total_events"`
	MatchedEvents  int               `json:"matched_events"`
	ReturnedEvents int               `json:"returned_events"`
	Truncated      bool              `json:"truncated"`
	Filters        map[string]string `json:"filters,omitempty"`
}

type Stats struct {
	TotalEvents     int       `json:"total_events"`
	TotalSessions   int       `json:"total_sessions"`
	Persistent      bool      `json:"persistent"`
	LatestCreatedAt time.Time `json:"latest_created_at,omitempty"`
}

type Recorder struct {
	mu     sync.RWMutex
	events map[string][]Event
	limit  int
	db     *sql.DB
}

func NewRecorder(limit int) *Recorder {
	if limit <= 0 {
		limit = 200
	}
	return &Recorder{events: map[string][]Event{}, limit: limit}
}

func NewSQLiteRecorder(db *sql.DB, limit int) *Recorder {
	recorder := NewRecorder(limit)
	recorder.db = db
	return recorder
}

func (r *Recorder) Persistent() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.db != nil
}

func (r *Recorder) Stats() (Stats, error) {
	if r == nil {
		return Stats{}, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.db != nil {
		return r.sqliteStatsLocked()
	}
	stats := Stats{}
	seenSessions := map[string]struct{}{}
	for sessionID, events := range r.events {
		if sessionID != "" && len(events) > 0 {
			seenSessions[sessionID] = struct{}{}
		}
		for _, event := range events {
			stats.TotalEvents++
			if event.SessionID != "" {
				seenSessions[event.SessionID] = struct{}{}
			}
			if stats.LatestCreatedAt.IsZero() || event.CreatedAt.After(stats.LatestCreatedAt) {
				stats.LatestCreatedAt = event.CreatedAt
			}
		}
	}
	stats.TotalSessions = len(seenSessions)
	return stats, nil
}

func (r *Recorder) Add(sessionID string, event Event) Event {
	event, _ = r.AddWithError(sessionID, event)
	return event
}

func (r *Recorder) AddWithError(sessionID string, event Event) (Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.SessionID == "" {
		event.SessionID = sessionID
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if event.ID == "" {
		event.ID = newEventID(event.CreatedAt)
	}
	event = redactEvent(event)
	items := append(r.events[sessionID], event)
	if len(items) > r.limit {
		items = items[len(items)-r.limit:]
	}
	r.events[sessionID] = items
	err := r.saveLocked(event)
	return event, err
}

func redactEvent(event Event) Event {
	event.Message = security.RedactSensitive(event.Message)
	event.Payload = redactPayload(event.Payload)
	return event
}

func redactPayload(payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return map[string]interface{}{
			"payload_redacted": true,
			"redaction_error":  security.RedactSensitive(err.Error()),
		}
	}
	var normalized interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return map[string]interface{}{
			"payload_redacted": true,
			"redaction_error":  security.RedactSensitive(err.Error()),
		}
	}
	redacted, ok := redactPayloadValue("", normalized).(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return redacted
}

func redactPayloadValue(key string, value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for childKey, childValue := range v {
			out[childKey] = redactPayloadValue(childKey, childValue)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = redactPayloadValue(key, item)
		}
		return out
	case string:
		if isSensitivePayloadKey(key) {
			return "[redacted]"
		}
		return security.RedactSensitive(v)
	default:
		return v
	}
}

func isSensitivePayloadKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization", "api_key", "apikey", "access_token", "refresh_token", "token", "secret", "client_secret", "password":
		return true
	default:
		return strings.HasSuffix(normalized, "_api_key") ||
			strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_secret")
	}
}

func newEventID(createdAt time.Time) string {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return createdAt.UTC().Format("20060102150405.000000000") + "_" + aitypes.NewID()
}

func (r *Recorder) List(sessionID string) []Event {
	events, _ := r.ListWithError(sessionID)
	return events
}

func (r *Recorder) ListWithError(sessionID string) ([]Event, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.events[sessionID]) == 0 && r.db != nil {
		return r.loadLocked(sessionID)
	}
	return append([]Event(nil), r.events[sessionID]...), nil
}

func (r *Recorder) Query(sessionID string, opts QueryOptions) QueryResult {
	result, _ := r.QueryWithError(sessionID, opts)
	return result
}

func (r *Recorder) QueryWithError(sessionID string, opts QueryOptions) (QueryResult, error) {
	events, err := r.ListWithError(sessionID)
	if err != nil {
		return QueryResult{}, err
	}
	opts = opts.normalized()
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if !opts.Since.IsZero() && event.CreatedAt.Before(opts.Since) {
			continue
		}
		if opts.Type != "" && !strings.EqualFold(event.Type, opts.Type) {
			continue
		}
		if opts.Status != "" && !strings.EqualFold(event.Status, opts.Status) {
			continue
		}
		filtered = append(filtered, event)
	}

	matched := len(filtered)
	truncated := false
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = append([]Event(nil), filtered[len(filtered)-opts.Limit:]...)
		truncated = true
	}
	return QueryResult{
		Events:         filtered,
		TotalEvents:    len(events),
		MatchedEvents:  matched,
		ReturnedEvents: len(filtered),
		Truncated:      truncated,
		Filters:        opts.Filters(),
	}, nil
}

func QueryOptionsFromParams(rawLimit, eventType, status string) QueryOptions {
	rawLimit = strings.TrimSpace(rawLimit)
	opts := QueryOptions{
		Type:   strings.TrimSpace(eventType),
		Status: strings.TrimSpace(status),
	}
	if rawLimit != "" {
		if limit, err := strconv.Atoi(rawLimit); err == nil {
			opts.Limit = limit
		}
	}
	return opts.normalized()
}

func ParseQueryOptionsFromParams(rawLimit, eventType, status, rawSinceHours string) (QueryOptions, error) {
	rawLimit = strings.TrimSpace(rawLimit)
	rawSinceHours = strings.TrimSpace(rawSinceHours)
	opts := QueryOptions{
		Type:   strings.TrimSpace(eventType),
		Status: strings.TrimSpace(status),
	}
	if len([]rune(opts.Type)) > MaxQueryFilterSize {
		return QueryOptions{}, fmt.Errorf("type must be at most %d characters", MaxQueryFilterSize)
	}
	if len([]rune(opts.Status)) > MaxQueryFilterSize {
		return QueryOptions{}, fmt.Errorf("status must be at most %d characters", MaxQueryFilterSize)
	}
	if rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return QueryOptions{}, fmt.Errorf("limit must be an integer between 1 and %d", MaxQueryLimit)
		}
		if limit <= 0 || limit > MaxQueryLimit {
			return QueryOptions{}, fmt.Errorf("limit must be between 1 and %d", MaxQueryLimit)
		}
		opts.Limit = limit
	}
	if rawSinceHours != "" {
		sinceHours, err := strconv.Atoi(rawSinceHours)
		if err != nil {
			return QueryOptions{}, fmt.Errorf("since_hours must be an integer between 1 and %d", MaxQuerySinceHours)
		}
		if sinceHours <= 0 || sinceHours > MaxQuerySinceHours {
			return QueryOptions{}, fmt.Errorf("since_hours must be between 1 and %d", MaxQuerySinceHours)
		}
		opts.SinceHours = sinceHours
		opts.Since = time.Now().Add(-time.Duration(sinceHours) * time.Hour)
	}
	return opts, nil
}

func (opts QueryOptions) Active() bool {
	opts = opts.normalized()
	return opts.Limit > 0 || opts.Type != "" || opts.Status != "" || !opts.Since.IsZero()
}

func (opts QueryOptions) Filters() map[string]string {
	filters := map[string]string{}
	opts = opts.normalized()
	if opts.Limit > 0 {
		filters["limit"] = strconv.Itoa(opts.Limit)
	}
	if opts.Type != "" {
		filters["type"] = opts.Type
	}
	if opts.Status != "" {
		filters["status"] = opts.Status
	}
	if opts.SinceHours > 0 {
		filters["since_hours"] = strconv.Itoa(opts.SinceHours)
	}
	if !opts.Since.IsZero() {
		filters["since"] = formatTime(opts.Since)
	}
	if len(filters) == 0 {
		return nil
	}
	return filters
}

func (opts QueryOptions) normalized() QueryOptions {
	opts.Type = strings.TrimSpace(opts.Type)
	opts.Status = strings.TrimSpace(opts.Status)
	if opts.Limit < 0 {
		opts.Limit = 0
	}
	if opts.Limit > MaxQueryLimit {
		opts.Limit = MaxQueryLimit
	}
	if opts.SinceHours < 0 {
		opts.SinceHours = 0
	}
	return opts
}

func (r *Recorder) DeleteSession(sessionID string) error {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		delete(r.events, sessionID)
		return nil
	}
	if _, err := r.db.Exec(`DELETE FROM trace_events WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	delete(r.events, sessionID)
	return nil
}

func (r *Recorder) saveLocked(event Event) error {
	if r.db == nil {
		return nil
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
INSERT OR REPLACE INTO trace_events
	(id, session_id, type, message, tool_name, status, duration_ms, payload_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, event.ID, event.SessionID, event.Type, event.Message, event.ToolName, event.Status, event.DurationMS, string(payload), formatTime(event.CreatedAt))
	if err != nil {
		return err
	}
	return r.pruneLocked(event.SessionID)
}

func (r *Recorder) pruneLocked(sessionID string) error {
	if r.db == nil || r.limit <= 0 {
		return nil
	}
	_, err := r.db.Exec(`
DELETE FROM trace_events
WHERE session_id = ?
  AND id NOT IN (
	SELECT id
	FROM trace_events
	WHERE session_id = ?
	ORDER BY created_at DESC, id DESC
	LIMIT ?
  )
`, sessionID, sessionID, r.limit)
	return err
}

func (r *Recorder) sqliteStatsLocked() (Stats, error) {
	stats := Stats{Persistent: true}
	var latestCreatedAt string
	err := r.db.QueryRow(`
SELECT COUNT(*), COUNT(DISTINCT session_id), COALESCE(MAX(created_at), '')
FROM trace_events
`).Scan(&stats.TotalEvents, &stats.TotalSessions, &latestCreatedAt)
	if err != nil {
		return Stats{}, err
	}
	stats.LatestCreatedAt = parseTime(latestCreatedAt)
	return stats, nil
}

func (r *Recorder) loadLocked(sessionID string) ([]Event, error) {
	if r.db == nil {
		return nil, nil
	}
	rows, err := r.db.Query(`
SELECT id, session_id, type, message, tool_name, status, duration_ms, payload_json, created_at
FROM trace_events
WHERE session_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?
`, sessionID, r.limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var event Event
		var payloadJSON, createdAt string
		if err := rows.Scan(&event.ID, &event.SessionID, &event.Type, &event.Message, &event.ToolName, &event.Status, &event.DurationMS, &payloadJSON, &createdAt); err != nil {
			return out, err
		}
		event.CreatedAt = parseTime(createdAt)
		if payloadJSON != "" {
			if err := json.Unmarshal([]byte(payloadJSON), &event.Payload); err != nil {
				return out, fmt.Errorf("parse trace payload: %w", err)
			}
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out) > 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		r.events[sessionID] = append([]Event(nil), out...)
	}
	return out, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
