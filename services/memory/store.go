package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"sort"
	"strings"
	"sync"
	"time"
)

type Session struct {
	ID        string            `json:"id"`
	Role      string            `json:"role,omitempty"`
	Messages  []aitypes.Message `json:"messages"`
	Summary   string            `json:"summary"`
	Facts     []string          `json:"facts"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type SessionSummary struct {
	ID                    string    `json:"id"`
	Role                  string    `json:"role,omitempty"`
	Title                 string    `json:"title,omitempty"`
	MessageCount          int       `json:"message_count"`
	UserMessageCount      int       `json:"user_message_count"`
	AssistantMessageCount int       `json:"assistant_message_count"`
	LastRole              string    `json:"last_role,omitempty"`
	LastMessagePreview    string    `json:"last_message_preview,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type SessionStats struct {
	TotalSessions             int       `json:"total_sessions"`
	TotalMessages             int       `json:"total_messages"`
	UserMessages              int       `json:"user_messages"`
	AssistantMessages         int       `json:"assistant_messages"`
	AverageMessagesPerSession float64   `json:"average_messages_per_session"`
	LatestUpdatedAt           time.Time `json:"latest_updated_at,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	maxMsgs  int
	db       *sql.DB
}

func NewStore(maxMsgs int) *Store {
	if maxMsgs <= 0 {
		maxMsgs = 80
	}
	return &Store{sessions: map[string]*Session{}, maxMsgs: maxMsgs}
}

func NewSQLiteStore(db *sql.DB, maxMsgs int) *Store {
	store := NewStore(maxMsgs)
	store.db = db
	return store
}

func (s *Store) Persistent() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db != nil
}

func (s *Store) GetOrCreate(sessionID string) *Session {
	session, _ := s.GetOrCreateWithError(sessionID)
	return session
}

func (s *Store) GetOrCreateWithError(sessionID string) (*Session, error) {
	return s.GetOrCreateForRoleWithError(sessionID, "")
}

func (s *Store) GetOrCreateForRoleWithError(sessionID, role string) (*Session, error) {
	if s == nil {
		return nil, fmt.Errorf("memory store is not configured")
	}
	role = normalizeSessionRole(role)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID != "" {
		if existing, ok := s.sessions[sessionID]; ok {
			if existing.Role == "" && role != "" {
				existing.Role = role
				if err := s.saveLocked(existing); err != nil {
					return nil, err
				}
			}
			return cloneSession(existing), nil
		}
		stored, err := s.loadWithErrorLocked(sessionID)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			if stored.Role == "" && role != "" {
				stored.Role = role
				if err := s.saveLocked(stored); err != nil {
					return nil, err
				}
			}
			s.sessions[sessionID] = stored
			return cloneSession(stored), nil
		}
	}
	now := time.Now()
	if sessionID == "" {
		sessionID = "ags_" + aitypes.NewID()
	}
	session := &Session{ID: sessionID, Role: role, CreatedAt: now, UpdatedAt: now}
	if err := s.saveLocked(session); err != nil {
		return nil, err
	}
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func (s *Store) Get(sessionID string) (*Session, bool, error) {
	if s == nil || sessionID == "" {
		return nil, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[sessionID]; ok {
		return cloneSession(existing), true, nil
	}
	if s.db == nil {
		return nil, false, nil
	}
	stored, err := s.loadWithErrorLocked(sessionID)
	if err != nil {
		return nil, false, err
	}
	if stored == nil {
		return nil, false, nil
	}
	s.sessions[sessionID] = stored
	return cloneSession(stored), true, nil
}

func normalizeSessionRole(role string) string {
	role = strings.TrimSpace(role)
	switch role {
	case string(aitypes.RoleCustomer), string(aitypes.RoleOperator), string(aitypes.RoleAdmin):
		return role
	default:
		return ""
	}
}

func (s *Store) Save(session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := cloneSession(session)
	if copy == nil {
		return nil
	}
	if len(copy.Messages) > s.maxMsgs {
		copy.Messages = copy.Messages[len(copy.Messages)-s.maxMsgs:]
	}
	copy.UpdatedAt = time.Now()
	if err := s.saveLocked(copy); err != nil {
		return err
	}
	s.sessions[copy.ID] = copy
	return nil
}

func (s *Store) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
			return err
		}
	}
	delete(s.sessions, sessionID)
	return nil
}

func (s *Store) ListSummaries(limit int) ([]SessionSummary, error) {
	return s.ListSummariesByRole(limit, "")
}

func (s *Store) ListSummariesByRole(limit int, role string) ([]SessionSummary, error) {
	if s == nil {
		return nil, nil
	}
	limit = normalizeSummaryLimit(limit)
	role, err := validateSummaryFilterRole(role)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db != nil {
		return s.listSQLiteSummariesLocked(limit, role)
	}
	summaries := make([]SessionSummary, 0, len(s.sessions))
	for _, session := range s.sessions {
		if !matchesSessionRole(session, role) {
			continue
		}
		if summary := summarizeSession(session); summary.ID != "" {
			summaries = append(summaries, summary)
		}
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (s *Store) ListSummariesUpdatedSince(limit int, since time.Time) ([]SessionSummary, error) {
	return s.ListSummariesUpdatedSinceByRole(limit, since, "")
}

func (s *Store) ListSummariesUpdatedSinceByRole(limit int, since time.Time, role string) ([]SessionSummary, error) {
	if s == nil {
		return nil, nil
	}
	if since.IsZero() {
		return s.ListSummariesByRole(limit, role)
	}
	limit = normalizeSummaryLimit(limit)
	role, err := validateSummaryFilterRole(role)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db != nil {
		return s.listSQLiteSummariesUpdatedSinceLocked(limit, since, role)
	}
	summaries := make([]SessionSummary, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session == nil || session.UpdatedAt.Before(since) {
			continue
		}
		if !matchesSessionRole(session, role) {
			continue
		}
		if summary := summarizeSession(session); summary.ID != "" {
			summaries = append(summaries, summary)
		}
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (s *Store) Stats() (SessionStats, error) {
	if s == nil {
		return SessionStats{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db != nil {
		return s.sqliteStatsLocked()
	}
	stats := SessionStats{}
	for _, session := range s.sessions {
		addSessionStats(&stats, session)
	}
	finalizeStats(&stats)
	return stats, nil
}

func (s *Store) CountActiveSessionsToday(role string) (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	datePrefix := time.Now().UTC().Format("2006-01-02") + "%"
	if s.db != nil {
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE role = ? AND (created_at LIKE ? OR updated_at LIKE ?)`, role, datePrefix, datePrefix).Scan(&count)
		return count, err
	}
	// In-memory fallback
	count := 0
	todayStr := time.Now().UTC().Format("2006-01-02")
	for _, session := range s.sessions {
		if session == nil {
			continue
		}
		if matchesSessionRole(session, role) {
			createdToday := session.CreatedAt.UTC().Format("2006-01-02") == todayStr
			updatedToday := session.UpdatedAt.UTC().Format("2006-01-02") == todayStr
			if createdToday || updatedToday {
				count++
			}
		}
	}
	return count, nil
}

func normalizeSummaryLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 50
	}
	return limit
}

func NormalizeSessionRoleFilter(role string) string {
	return normalizeSummaryFilterRole(role)
}

func normalizeSummaryFilterRole(role string) string {
	role = strings.TrimSpace(role)
	switch role {
	case string(aitypes.RoleCustomer), string(aitypes.RoleOperator), string(aitypes.RoleAdmin):
		return role
	case "legacy":
		return "legacy"
	default:
		return ""
	}
}

func validateSummaryFilterRole(role string) (string, error) {
	if strings.TrimSpace(role) == "" {
		return "", nil
	}
	normalized := normalizeSummaryFilterRole(role)
	if normalized == "" {
		return "", fmt.Errorf("role must be one of customer, operator, admin, legacy")
	}
	return normalized, nil
}

func matchesSessionRole(session *Session, role string) bool {
	if role == "" {
		return true
	}
	if session == nil {
		return false
	}
	if role == "legacy" {
		return normalizeSessionRole(session.Role) == ""
	}
	return normalizeSessionRole(session.Role) == role
}

func (s *Store) DeleteOlderThan(cutoff time.Time) ([]string, error) {
	return s.DeleteOlderThanByRole(cutoff, "")
}

func (s *Store) DeleteOlderThanByRole(cutoff time.Time, role string) ([]string, error) {
	if s == nil || cutoff.IsZero() {
		return nil, nil
	}
	role, err := validateSummaryFilterRole(role)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted, err := s.listOlderThanLocked(cutoff, role)
	if err != nil {
		return nil, err
	}
	if len(deleted) == 0 {
		return deleted, nil
	}
	cutoffValue := formatTime(cutoff)
	if s.db != nil {
		query := `DELETE FROM sessions WHERE updated_at < ?`
		args := []interface{}{cutoffValue}
		if role == "legacy" {
			query += ` AND role = ''`
		} else if role != "" {
			query += ` AND role = ?`
			args = append(args, role)
		}
		if _, err := s.db.Exec(query, args...); err != nil {
			return nil, err
		}
	}
	for _, id := range deleted {
		delete(s.sessions, id)
	}
	return deleted, nil
}

func (s *Store) ListOlderThan(cutoff time.Time) ([]string, error) {
	return s.ListOlderThanByRole(cutoff, "")
}

func (s *Store) ListOlderThanByRole(cutoff time.Time, role string) ([]string, error) {
	if s == nil || cutoff.IsZero() {
		return nil, nil
	}
	role, err := validateSummaryFilterRole(role)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listOlderThanLocked(cutoff, role)
}

func (s *Store) listOlderThanLocked(cutoff time.Time, role string) ([]string, error) {
	cutoffValue := formatTime(cutoff)
	deleted := make([]string, 0)
	seen := map[string]struct{}{}
	addDeleted := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		deleted = append(deleted, id)
	}
	if s.db != nil {
		query := `SELECT id FROM sessions WHERE updated_at < ?`
		args := []interface{}{cutoffValue}
		if role == "legacy" {
			query += ` AND role = ''`
		} else if role != "" {
			query += ` AND role = ?`
			args = append(args, role)
		}
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			addDeleted(id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	for id, session := range s.sessions {
		if session != nil && session.UpdatedAt.Before(cutoff) {
			if !matchesSessionRole(session, role) {
				continue
			}
			if s.db != nil {
				var persistedID string
				err := s.db.QueryRow(`SELECT id FROM sessions WHERE id = ?`, id).Scan(&persistedID)
				if err == nil {
					continue
				}
				if err != sql.ErrNoRows {
					return nil, err
				}
			}
			addDeleted(id)
		}
	}
	return deleted, nil
}

func (s *Store) sqliteStatsLocked() (SessionStats, error) {
	rows, err := s.db.Query(`SELECT messages_json, updated_at FROM sessions`)
	if err != nil {
		return SessionStats{}, err
	}
	defer rows.Close()
	stats := SessionStats{}
	for rows.Next() {
		var messagesJSON, updatedAt string
		if err := rows.Scan(&messagesJSON, &updatedAt); err != nil {
			return SessionStats{}, err
		}
		session := &Session{UpdatedAt: parseTime(updatedAt)}
		if err := parseSessionMessages(messagesJSON, &session.Messages); err != nil {
			return SessionStats{}, err
		}
		addSessionStats(&stats, session)
	}
	if err := rows.Err(); err != nil {
		return SessionStats{}, err
	}
	finalizeStats(&stats)
	return stats, nil
}

func (s *Store) listSQLiteSummariesLocked(limit int, role string) ([]SessionSummary, error) {
	query := `
SELECT id, role, messages_json, created_at, updated_at
FROM sessions
`
	args := []interface{}{}
	if role == "legacy" {
		query += "WHERE role = ''\n"
	} else if role != "" {
		query += "WHERE role = ?\n"
		args = append(args, role)
	}
	query += `
ORDER BY updated_at DESC
LIMIT ?
`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]SessionSummary, 0, limit)
	for rows.Next() {
		var id, role, messagesJSON, createdAt, updatedAt string
		if err := rows.Scan(&id, &role, &messagesJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		session := &Session{
			ID:        id,
			Role:      normalizeSessionRole(role),
			CreatedAt: parseTime(createdAt),
			UpdatedAt: parseTime(updatedAt),
		}
		if err := parseSessionMessages(messagesJSON, &session.Messages); err != nil {
			return nil, err
		}
		summaries = append(summaries, summarizeSession(session))
	}
	return summaries, rows.Err()
}

func (s *Store) listSQLiteSummariesUpdatedSinceLocked(limit int, since time.Time, role string) ([]SessionSummary, error) {
	query := `
SELECT id, role, messages_json, created_at, updated_at
FROM sessions
WHERE updated_at >= ?
`
	args := []interface{}{formatTime(since)}
	if role == "legacy" {
		query += "AND role = ''\n"
	} else if role != "" {
		query += "AND role = ?\n"
		args = append(args, role)
	}
	query += `
ORDER BY updated_at DESC
LIMIT ?
`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]SessionSummary, 0, limit)
	for rows.Next() {
		var id, role, messagesJSON, createdAt, updatedAt string
		if err := rows.Scan(&id, &role, &messagesJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		session := &Session{
			ID:        id,
			Role:      normalizeSessionRole(role),
			CreatedAt: parseTime(createdAt),
			UpdatedAt: parseTime(updatedAt),
		}
		if err := parseSessionMessages(messagesJSON, &session.Messages); err != nil {
			return nil, err
		}
		summaries = append(summaries, summarizeSession(session))
	}
	return summaries, rows.Err()
}

func (s *Store) loadWithErrorLocked(sessionID string) (*Session, error) {
	if s.db == nil {
		return nil, nil
	}
	var role, messagesJSON, factsJSON string
	var createdAt, updatedAt string
	session := &Session{ID: sessionID}
	err := s.db.QueryRow(`SELECT role, messages_json, summary, facts_json, created_at, updated_at FROM sessions WHERE id = ?`, sessionID).
		Scan(&role, &messagesJSON, &session.Summary, &factsJSON, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session.Role = normalizeSessionRole(role)
	session.CreatedAt = parseTime(createdAt)
	session.UpdatedAt = parseTime(updatedAt)
	if err := parseSessionMessages(messagesJSON, &session.Messages); err != nil {
		return nil, err
	}
	if err := parseSessionFacts(factsJSON, &session.Facts); err != nil {
		return nil, err
	}
	return session, nil
}

func parseSessionMessages(raw string, out *[]aitypes.Message) error {
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("parse session messages: %w", err)
	}
	return nil
}

func parseSessionFacts(raw string, out *[]string) error {
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("parse session facts: %w", err)
	}
	return nil
}

func (s *Store) saveLocked(session *Session) error {
	if s.db == nil || session == nil {
		return nil
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now()
	}
	messages, err := json.Marshal(session.Messages)
	if err != nil {
		return err
	}
	facts, err := json.Marshal(session.Facts)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO sessions (id, role, messages_json, summary, facts_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	role = CASE WHEN excluded.role != '' THEN excluded.role ELSE sessions.role END,
	messages_json = excluded.messages_json,
	summary = excluded.summary,
	facts_json = excluded.facts_json,
	updated_at = excluded.updated_at
`, session.ID, normalizeSessionRole(session.Role), string(messages), session.Summary, string(facts), formatTime(session.CreatedAt), formatTime(session.UpdatedAt))
	return err
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

func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}
	cp := *session
	cp.Messages = append([]aitypes.Message(nil), session.Messages...)
	cp.Facts = append([]string(nil), session.Facts...)
	return &cp
}

func summarizeSession(session *Session) SessionSummary {
	if session == nil {
		return SessionSummary{}
	}
	summary := SessionSummary{
		ID:           session.ID,
		Role:         normalizeSessionRole(session.Role),
		Title:        TitleFromMessages(session.Messages),
		MessageCount: len(session.Messages),
		CreatedAt:    session.CreatedAt,
		UpdatedAt:    session.UpdatedAt,
	}
	for _, message := range session.Messages {
		switch message.Role {
		case aitypes.RoleUser:
			summary.UserMessageCount++
		case aitypes.RoleAssistant:
			summary.AssistantMessageCount++
		}
	}
	if len(session.Messages) > 0 {
		last := session.Messages[len(session.Messages)-1]
		summary.LastRole = string(last.Role)
		summary.LastMessagePreview = truncateRunes(strings.TrimSpace(last.Content), 120)
	}
	return summary
}

// TitleFromMessages returns the stable session title generated from the first user question.
func TitleFromMessages(messages []aitypes.Message) string {
	for _, message := range messages {
		if message.Role != aitypes.RoleUser {
			continue
		}
		title := compactWhitespace(message.Content)
		if title != "" {
			return truncateRunes(title, 120)
		}
	}
	return ""
}

func compactWhitespace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func addSessionStats(stats *SessionStats, session *Session) {
	if stats == nil || session == nil {
		return
	}
	stats.TotalSessions++
	stats.TotalMessages += len(session.Messages)
	for _, message := range session.Messages {
		switch message.Role {
		case aitypes.RoleUser:
			stats.UserMessages++
		case aitypes.RoleAssistant:
			stats.AssistantMessages++
		}
	}
	if stats.LatestUpdatedAt.IsZero() || session.UpdatedAt.After(stats.LatestUpdatedAt) {
		stats.LatestUpdatedAt = session.UpdatedAt
	}
}

func finalizeStats(stats *SessionStats) {
	if stats == nil || stats.TotalSessions == 0 {
		return
	}
	stats.AverageMessagesPerSession = float64(stats.TotalMessages) / float64(stats.TotalSessions)
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}
