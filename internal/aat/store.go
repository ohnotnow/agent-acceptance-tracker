package aat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Feature struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type FeatureRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Story struct {
	ID          string     `json:"id"`
	Feature     FeatureRef `json:"feature"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	CreatedAt   string     `json:"created_at"`
	Scenarios   []Scenario `json:"scenarios"`
}

type Scenario struct {
	ID          string `json:"id"`
	StoryID     string `json:"story_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Lines       []Line `json:"lines"`
	Status      string `json:"status"`
	TestFile    string `json:"test_file,omitempty"` // relative to the project root
	Test        string `json:"test,omitempty"`
	CreatedAt   string `json:"created_at"`
	ClosedAt    string `json:"closed_at,omitempty"`
}

type migration struct {
	version     int
	description string
	apply       func(ctx context.Context, tx *sql.Tx) error
}

// Append new migrations here. Never modify existing ones.
var migrations = []migration{
	{1, "features, stories and scenarios", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE features (
				id          TEXT PRIMARY KEY,
				title       TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL
			);
			CREATE TABLE stories (
				id          TEXT PRIMARY KEY,
				feature_id  TEXT NOT NULL REFERENCES features(id),
				title       TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL
			);
			CREATE TABLE scenarios (
				id          TEXT PRIMARY KEY,
				story_id    TEXT NOT NULL REFERENCES stories(id),
				position    INTEGER NOT NULL,
				title       TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				UNIQUE (story_id, position)
			);
			CREATE TABLE scenario_lines (
				scenario_id TEXT NOT NULL REFERENCES scenarios(id),
				position    INTEGER NOT NULL,
				keyword     TEXT NOT NULL CHECK (keyword IN ('Given', 'When', 'Then', 'And', 'But')),
				text        TEXT NOT NULL,
				PRIMARY KEY (scenario_id, position)
			);`)
		return err
	}},
	{2, "closing a scenario with its test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			ALTER TABLE scenarios ADD COLUMN test_file TEXT NOT NULL DEFAULT '';
			ALTER TABLE scenarios ADD COLUMN test_name TEXT NOT NULL DEFAULT '';
			ALTER TABLE scenarios ADD COLUMN closed_at TEXT;`)
		return err
	}},
}

func OpenStore(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_version (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL DEFAULT 0
		);
		INSERT OR IGNORE INTO schema_version (id, version) VALUES (1, 0);`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE id = 1").Scan(&current); err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.inTx(ctx, func(tx *sql.Tx) error {
			if err := m.apply(ctx, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE schema_version SET version = ? WHERE id = 1", m.version)
			return err
		}); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.description, err)
		}
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Store) AddFeature(ctx context.Context, title, description string) (Feature, error) {
	id, err := newID(featureIDLen)
	if err != nil {
		return Feature{}, err
	}
	f := Feature{ID: id, Title: title, Description: description, CreatedAt: now()}
	_, err = s.db.ExecContext(ctx, "INSERT INTO features (id, title, description, created_at) VALUES (?, ?, ?, ?)",
		f.ID, f.Title, f.Description, f.CreatedAt)
	return f, err
}

// ListFeatures returns every feature, oldest first, without descriptions.
func (s *Store) ListFeatures(ctx context.Context) ([]Feature, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, title, created_at FROM features ORDER BY created_at, rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	features := []Feature{}
	for rows.Next() {
		var f Feature
		if err := rows.Scan(&f.ID, &f.Title, &f.CreatedAt); err != nil {
			return nil, err
		}
		features = append(features, f)
	}
	return features, rows.Err()
}

func (s *Store) featureRef(ctx context.Context, id string) (FeatureRef, error) {
	ref := FeatureRef{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT title FROM features WHERE id = ?", id).Scan(&ref.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, notFoundErr("no feature %q", id)
	}
	return ref, err
}

func (s *Store) AddStory(ctx context.Context, featureID, title, description string) (Story, error) {
	ref, err := s.featureRef(ctx, featureID)
	if err != nil {
		return Story{}, err
	}
	id, err := newID(storyIDLen)
	if err != nil {
		return Story{}, err
	}
	st := Story{ID: id, Feature: ref, Title: title, Description: description, CreatedAt: now(), Scenarios: []Scenario{}}
	_, err = s.db.ExecContext(ctx, "INSERT INTO stories (id, feature_id, title, description, created_at) VALUES (?, ?, ?, ?, ?)",
		st.ID, featureID, st.Title, st.Description, st.CreatedAt)
	return st, err
}

// GetStory returns a story with its feature and its scenarios in order.
func (s *Store) GetStory(ctx context.Context, id string) (Story, error) {
	st := Story{ID: id}
	err := s.db.QueryRowContext(ctx, `
		SELECT st.title, st.description, st.created_at, f.id, f.title
		FROM stories st JOIN features f ON f.id = st.feature_id
		WHERE st.id = ?`, id).Scan(&st.Title, &st.Description, &st.CreatedAt, &st.Feature.ID, &st.Feature.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return st, notFoundErr("no story %q", id)
	}
	if err != nil {
		return st, err
	}
	st.Scenarios, err = s.loadScenarios(ctx, "WHERE story_id = ? ORDER BY position", id)
	return st, err
}

func (s *Store) requireStory(ctx context.Context, id string) error {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM stories WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return notFoundErr("no story %q", id)
	}
	return err
}

// AddScenarios appends scenarios to the end of a story, all or nothing.
func (s *Store) AddScenarios(ctx context.Context, storyID string, parsed []ParsedScenario) ([]Scenario, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return nil, err
	}
	var added []Scenario
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var pos int
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0) FROM scenarios WHERE story_id = ?", storyID).Scan(&pos); err != nil {
			return err
		}
		for _, p := range parsed {
			id, err := newID(scenarioIDLen)
			if err != nil {
				return err
			}
			pos++
			sc := Scenario{ID: id, StoryID: storyID, Title: p.Title, Description: p.Description, Lines: p.Lines, Status: "open", CreatedAt: now()}
			if _, err := tx.ExecContext(ctx, "INSERT INTO scenarios (id, story_id, position, title, description, created_at) VALUES (?, ?, ?, ?, ?, ?)",
				sc.ID, storyID, pos, sc.Title, sc.Description, sc.CreatedAt); err != nil {
				return err
			}
			for i, l := range p.Lines {
				if _, err := tx.ExecContext(ctx, "INSERT INTO scenario_lines (scenario_id, position, keyword, text) VALUES (?, ?, ?, ?)",
					sc.ID, i+1, l.Keyword, l.Text); err != nil {
					return err
				}
			}
			added = append(added, sc)
		}
		return nil
	})
	return added, err
}

// NextScenario returns the story's first open scenario, or nil when there is none.
func (s *Store) NextScenario(ctx context.Context, storyID string) (*Scenario, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return nil, err
	}
	scs, err := s.loadScenarios(ctx, "WHERE story_id = ? AND closed_at IS NULL ORDER BY position LIMIT 1", storyID)
	if err != nil || len(scs) == 0 {
		return nil, err
	}
	return &scs[0], nil
}

func (s *Store) GetScenario(ctx context.Context, id string) (Scenario, error) {
	scs, err := s.loadScenarios(ctx, "WHERE id = ?", id)
	if err != nil {
		return Scenario{}, err
	}
	if len(scs) == 0 {
		return Scenario{}, notFoundErr("no scenario %q", id)
	}
	return scs[0], nil
}

// CloseScenario records the test that proves an open scenario. It also
// returns the ids of other closed scenarios that name the same test.
func (s *Store) CloseScenario(ctx context.Context, id, testFile, testName string) ([]string, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scenarios SET test_file = ?, test_name = ?, closed_at = ?
		WHERE id = ? AND closed_at IS NULL`, testFile, testName, now(), id)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = conflictErr("scenario %q is already closed", id)
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM scenarios
		WHERE test_file = ? AND test_name = ? AND closed_at IS NOT NULL AND id != ?
		ORDER BY closed_at, rowid`, testFile, testName, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var others []string
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return nil, err
		}
		others = append(others, other)
	}
	return others, rows.Err()
}

// loadScenarios runs a scenario query with the given WHERE/ORDER clause and loads each one's lines.
func (s *Store) loadScenarios(ctx context.Context, clause string, args ...any) ([]Scenario, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, story_id, title, description, test_file, test_name, created_at, COALESCE(closed_at, '')
		FROM scenarios `+clause, args...)
	if err != nil {
		return nil, err
	}
	scs := []Scenario{}
	for rows.Next() {
		sc := Scenario{Lines: []Line{}, Status: "open"}
		if err := rows.Scan(&sc.ID, &sc.StoryID, &sc.Title, &sc.Description, &sc.TestFile, &sc.Test, &sc.CreatedAt, &sc.ClosedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if sc.ClosedAt != "" {
			sc.Status = "closed"
		}
		scs = append(scs, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range scs {
		lrows, err := s.db.QueryContext(ctx, "SELECT keyword, text FROM scenario_lines WHERE scenario_id = ? ORDER BY position", scs[i].ID)
		if err != nil {
			return nil, err
		}
		for lrows.Next() {
			var l Line
			if err := lrows.Scan(&l.Keyword, &l.Text); err != nil {
				lrows.Close()
				return nil, err
			}
			scs[i].Lines = append(scs[i].Lines, l)
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			return nil, err
		}
	}
	return scs, nil
}
