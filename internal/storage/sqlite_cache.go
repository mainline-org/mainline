package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mainline-org/mainline/internal/domain"
)

var ErrMainlineIndexUnavailable = errors.New("mainline sqlite index unavailable")

type IndexedLogIntent struct {
	IntentID   string
	Status     domain.IntentStatus
	Title      string
	Goal       string
	Thread     string
	SealedAt   string
	ActivityAt string
	Author     string
	ActorID    string
	ActorName  string
	LastCheck  *domain.CheckSummary
}

func (s *Store) mainlineIndexPath() string {
	return filepath.Join(s.viewsDir(), "mainline.db")
}

func (s *Store) RebuildMainlineIndex(view *domain.MainlineView) error {
	if view == nil {
		return nil
	}
	if err := os.MkdirAll(s.viewsDir(), 0o755); err != nil {
		return err
	}

	path := s.mainlineIndexPath()
	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return err
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	if err := initialiseMainlineIndex(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := writeMainlineIndex(tx, s, view); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	db = nil
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func (s *Store) ReadIndexedLogIntents(statusFilter domain.IntentStatus) ([]IndexedLogIntent, error) {
	if _, err := os.Stat(s.mainlineIndexPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMainlineIndexUnavailable
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", s.mainlineIndexPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT intent_id, status, title, goal, thread, sealed_at, activity_at,
		author, actor_id, actor_name, last_check_json
		FROM intents`
	var args []any
	if statusFilter != "" {
		query += ` WHERE status = ?`
		args = append(args, string(statusFilter))
	}
	query += ` ORDER BY activity_at DESC, intent_id ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IndexedLogIntent
	for rows.Next() {
		var item IndexedLogIntent
		var status string
		var lastCheck sql.NullString
		if err := rows.Scan(
			&item.IntentID,
			&status,
			&item.Title,
			&item.Goal,
			&item.Thread,
			&item.SealedAt,
			&item.ActivityAt,
			&item.Author,
			&item.ActorID,
			&item.ActorName,
			&lastCheck,
		); err != nil {
			return nil, err
		}
		item.Status = domain.IntentStatus(status)
		if lastCheck.Valid && lastCheck.String != "" {
			var lc domain.CheckSummary
			if err := json.Unmarshal([]byte(lastCheck.String), &lc); err != nil {
				return nil, err
			}
			item.LastCheck = &lc
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadIntentViewByID returns the IntentView for one specific id by
// hitting the SQLite primary key index. Falls through to
// ErrMainlineIndexUnavailable when the cache is missing — callers
// then load the full JSON view and scan.
//
// Semantics are identical to scanning the JSON view: we deserialise
// the raw_json column (the same struct WriteMainlineView serialised
// into the JSON file on the same sync). No projection trimming.
func (s *Store) ReadIntentViewByID(id string) (*domain.IntentView, error) {
	if _, err := os.Stat(s.mainlineIndexPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMainlineIndexUnavailable
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", s.mainlineIndexPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	row := db.QueryRow(`SELECT raw_json FROM intents WHERE intent_id = ?`, id)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var iv domain.IntentView
	if err := json.Unmarshal([]byte(raw), &iv); err != nil {
		return nil, err
	}
	return &iv, nil
}

// ReadIntentViewsByFiles returns every IntentView whose
// fingerprint.files_touched intersects the given paths, using the
// intent_files reverse index. The hot path for `mainline context
// --files` — shrinks the candidate set from "every sealed intent"
// to "intents that touched at least one queried file".
//
// Returns ErrMainlineIndexUnavailable when the cache is missing.
// Returns no rows (not an error) when no intent matches.
func (s *Store) ReadIntentViewsByFiles(paths []string) ([]domain.IntentView, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(s.mainlineIndexPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMainlineIndexUnavailable
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", s.mainlineIndexPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Build placeholder set for the IN clause. SQLite's
	// argument-positional ? doesn't variadicize, so we expand by
	// hand. paths comes from the agent's --files flag — bounded by
	// the diff size, never thousands.
	placeholders := strings.Repeat("?,", len(paths))
	placeholders = placeholders[:len(placeholders)-1]
	query := `SELECT DISTINCT i.raw_json
		FROM intents i
		JOIN intent_files f ON f.intent_id = i.intent_id
		WHERE f.path IN (` + placeholders + `)
		ORDER BY i.activity_at DESC, i.intent_id ASC`
	args := make([]any, 0, len(paths))
	for _, p := range paths {
		args = append(args, p)
	}
	return scanIntentViews(db, query, args)
}

// ReadIntentViewsByQuery returns every IntentView whose title /
// what / decision text / risk text contains the given keyword
// (case-insensitive substring). Hot path for `mainline context
// --query`. Substring rather than FTS because the corpus is small
// (hundreds of intents) and SQLite LIKE on indexed columns is
// already fast at this scale.
//
// Returns ErrMainlineIndexUnavailable when the cache is missing.
func (s *Store) ReadIntentViewsByQuery(keyword string) ([]domain.IntentView, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.mainlineIndexPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMainlineIndexUnavailable
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", s.mainlineIndexPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	pattern := "%" + strings.ToLower(keyword) + "%"
	query := `SELECT DISTINCT i.raw_json
		FROM intents i
		LEFT JOIN intent_decisions d ON d.intent_id = i.intent_id
		LEFT JOIN intent_risks r ON r.intent_id = i.intent_id
		WHERE LOWER(i.title) LIKE ?
		   OR LOWER(i.goal) LIKE ?
		   OR LOWER(d.text) LIKE ?
		   OR LOWER(r.text) LIKE ?
		ORDER BY i.activity_at DESC, i.intent_id ASC`
	args := []any{pattern, pattern, pattern, pattern}
	return scanIntentViews(db, query, args)
}

// scanIntentViews unmarshals the raw_json column from each result
// row. Shared by the two query helpers above so the deserialise
// path stays in one place.
func scanIntentViews(db *sql.DB, query string, args []any) ([]domain.IntentView, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IntentView
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var iv domain.IntentView
		if err := json.Unmarshal([]byte(raw), &iv); err != nil {
			return nil, err
		}
		out = append(out, iv)
	}
	return out, rows.Err()
}

func initialiseMainlineIndex(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = OFF`,
		`PRAGMA synchronous = OFF`,
		`PRAGMA temp_store = MEMORY`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE intents (
			intent_id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			title TEXT NOT NULL,
			goal TEXT NOT NULL,
			thread TEXT NOT NULL,
			git_branch TEXT NOT NULL,
			sealed_at TEXT NOT NULL,
			activity_at TEXT NOT NULL,
			author TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			actor_name TEXT NOT NULL,
			merged_main_commit TEXT NOT NULL,
			superseded_by_intent TEXT NOT NULL,
			last_check_json TEXT,
			raw_json TEXT NOT NULL
		)`,
		`CREATE TABLE intent_files (
			intent_id TEXT NOT NULL,
			path TEXT NOT NULL,
			PRIMARY KEY (intent_id, path)
		)`,
		`CREATE TABLE intent_tags (
			intent_id TEXT NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (intent_id, tag)
		)`,
		`CREATE TABLE intent_subsystems (
			intent_id TEXT NOT NULL,
			subsystem TEXT NOT NULL,
			PRIMARY KEY (intent_id, subsystem)
		)`,
		`CREATE TABLE intent_decisions (
			intent_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			text TEXT NOT NULL,
			PRIMARY KEY (intent_id, idx)
		)`,
		`CREATE TABLE intent_risks (
			intent_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			text TEXT NOT NULL,
			PRIMARY KEY (intent_id, idx)
		)`,
		`CREATE TABLE intent_anti_patterns (
			intent_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			what TEXT NOT NULL,
			why TEXT NOT NULL,
			severity TEXT NOT NULL,
			PRIMARY KEY (intent_id, idx)
		)`,
		`CREATE INDEX idx_intents_activity ON intents(activity_at DESC, intent_id ASC)`,
		`CREATE INDEX idx_intents_status_activity ON intents(status, activity_at DESC, intent_id ASC)`,
		`CREATE INDEX idx_intent_files_path ON intent_files(path, intent_id)`,
		`CREATE INDEX idx_intent_tags_tag ON intent_tags(tag, intent_id)`,
		`CREATE INDEX idx_intent_subsystems_subsystem ON intent_subsystems(subsystem, intent_id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func writeMainlineIndex(tx *sql.Tx, store *Store, view *domain.MainlineView) error {
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES
		('schema_version', '1'),
		('rebuilt_at', ?),
		('main_branch', ?),
		('main_head', ?)`,
		view.RebuiltAt, view.MainBranch, view.MainHead); err != nil {
		return err
	}

	insertIntent, err := tx.Prepare(`INSERT INTO intents (
		intent_id, status, title, goal, thread, git_branch, sealed_at,
		activity_at, author, actor_id, actor_name, merged_main_commit,
		superseded_by_intent, last_check_json, raw_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertIntent.Close()

	insertFile, err := tx.Prepare(`INSERT OR IGNORE INTO intent_files(intent_id, path) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertFile.Close()

	insertTag, err := tx.Prepare(`INSERT OR IGNORE INTO intent_tags(intent_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertTag.Close()

	insertSubsystem, err := tx.Prepare(`INSERT OR IGNORE INTO intent_subsystems(intent_id, subsystem) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertSubsystem.Close()

	insertDecision, err := tx.Prepare(`INSERT INTO intent_decisions(intent_id, idx, text) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertDecision.Close()

	insertRisk, err := tx.Prepare(`INSERT INTO intent_risks(intent_id, idx, text) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertRisk.Close()

	insertAntiPattern, err := tx.Prepare(`INSERT INTO intent_anti_patterns(intent_id, idx, what, why, severity) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertAntiPattern.Close()

	for _, iv := range view.Intents {
		title := ""
		if iv.Summary != nil {
			title = iv.Summary.Title
		}
		raw, err := json.Marshal(iv)
		if err != nil {
			return err
		}
		var lastCheck any
		if iv.LastCheck != nil {
			data, err := json.Marshal(iv.LastCheck)
			if err != nil {
				return err
			}
			lastCheck = string(data)
		}
		if _, err := insertIntent.Exec(
			iv.IntentID,
			string(iv.Status),
			title,
			iv.Goal,
			iv.Thread,
			iv.GitBranch,
			iv.SealedAt,
			store.indexedActivityAt(iv),
			authorName(iv.ActorName, iv.ActorID),
			iv.ActorID,
			iv.ActorName,
			iv.StatusEvidence.MergedMainCommit,
			iv.StatusEvidence.SupersededByIntent,
			lastCheck,
			string(raw),
		); err != nil {
			return err
		}
		if iv.Fingerprint != nil {
			if err := insertStringSet(insertFile, iv.IntentID, iv.Fingerprint.FilesTouched); err != nil {
				return err
			}
			if err := insertStringSet(insertTag, iv.IntentID, iv.Fingerprint.Tags); err != nil {
				return err
			}
			if err := insertStringSet(insertSubsystem, iv.IntentID, iv.Fingerprint.Subsystems); err != nil {
				return err
			}
		}
		if iv.Summary != nil {
			for i, d := range iv.Summary.Decisions {
				if _, err := insertDecision.Exec(iv.IntentID, i, decisionText(d)); err != nil {
					return err
				}
			}
			for i, risk := range iv.Summary.Risks {
				if _, err := insertRisk.Exec(iv.IntentID, i, risk); err != nil {
					return err
				}
			}
			for i, ap := range iv.Summary.AntiPatterns {
				if _, err := insertAntiPattern.Exec(iv.IntentID, i, ap.What, ap.Why, ap.Severity); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) indexedActivityAt(iv domain.IntentView) string {
	if s.Git != nil {
		switch iv.Status {
		case domain.StatusMerged:
			if iv.StatusEvidence.MergedMainCommit != "" {
				if date, err := s.Git.CommitDate(iv.StatusEvidence.MergedMainCommit); err == nil {
					return date
				}
			}
		case domain.StatusReverted:
			if iv.StatusEvidence.RevertedMainCommit != "" {
				if date, err := s.Git.CommitDate(iv.StatusEvidence.RevertedMainCommit); err == nil {
					return date
				}
			}
		}
	}
	if iv.SealedAt != "" {
		return iv.SealedAt
	}
	return iv.ViewRebuiltAt
}

func insertStringSet(stmt *sql.Stmt, intentID string, values []string) error {
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, err := stmt.Exec(intentID, value); err != nil {
			return err
		}
	}
	return nil
}

func authorName(actorName, actorID string) string {
	if actorName != "" {
		return actorName
	}
	return actorID
}

func decisionText(d domain.Decision) string {
	parts := []string{d.Point, d.Chose, d.Rationale}
	return strings.TrimSpace(strings.Join(parts, " "))
}
