package aat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	Steps       []Step     `json:"steps"`                 // scenarios and planned check-ins, in the story's order
	Questions   []Question `json:"questions"`             // open questions asked mid-build, without rounds
	Retires     []Scenario `json:"retires,omitempty"`     // scenarios this story retired, whose behaviour and tests it removes
	AcceptedAt  string     `json:"accepted_at,omitempty"` // when the user reviewed and accepted the finished story
	AcceptNote  string     `json:"accept_note,omitempty"`
}

// Step is one place in a story's order: a scenario, or a planned check-in
// (open or settled, without its rounds). It has the same shape as next's
// output.
type Step struct {
	Scenario *Scenario `json:"scenario,omitempty"`
	Question *Question `json:"question,omitempty"`
}

// Position is the step's place in the story's order.
func (st Step) Position() int {
	if st.Scenario != nil {
		return st.Scenario.Position
	}
	return st.Question.Position
}

type Scenario struct {
	ID          string `json:"id"`
	StoryID     string `json:"story_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Lines       []Line `json:"lines"`
	Position    int    `json:"position"` // place in the story's order, shared with planned questions
	Status      string `json:"status"`
	TestFile    string `json:"test_file,omitempty"` // relative to the project root
	Test        string `json:"test,omitempty"`
	QuestionID  string `json:"question_id,omitempty"` // the question this scenario came out of, if any
	CreatedAt   string `json:"created_at"`
	ClosedAt    string `json:"closed_at,omitempty"`

	ReplacedBy string    `json:"replaced_by,omitempty"`   // the scenario that replaced this one
	RetiredBy  string    `json:"retired_by,omitempty"`    // the story that retired this one
	Reason     string    `json:"reason,omitempty"`        // why it was replaced or retired
	RemovedAt  string    `json:"removed_at,omitempty"`    // when a retired scenario's behaviour and test were removed
	HandedOut  string    `json:"handed_out_at,omitempty"` // when next first handed it to the builder; from then on it is no longer a draft
	Replaces   *Replaced `json:"replaces,omitempty"`      // the scenario this one replaced
}

// Replaced is the scenario a newer one replaced: its rule, the test that
// proved it (which the builder of the new one has to change or remove), and
// why it was replaced.
type Replaced struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Lines    []Line `json:"lines"`
	TestFile string `json:"test_file,omitempty"`
	Test     string `json:"test,omitempty"`
	Reason   string `json:"reason"`
}

// current is the SQL condition for a scenario that is still part of the spec.
const current = "replaced_by IS NULL AND retired_by IS NULL"

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
	{3, "questions and their rounds", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE questions (
				id          TEXT PRIMARY KEY,
				story_id    TEXT NOT NULL REFERENCES stories(id),
				text        TEXT NOT NULL,
				note        TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				resolved_at TEXT
			);
			CREATE TABLE question_rounds (
				question_id TEXT NOT NULL REFERENCES questions(id),
				position    INTEGER NOT NULL,
				answer      TEXT NOT NULL,
				created_at  TEXT NOT NULL,
				PRIMARY KEY (question_id, position)
			);
			ALTER TABLE scenarios ADD COLUMN question_id TEXT REFERENCES questions(id);`)
		return err
	}},
	{4, "planned questions take a place in the story's order", func(ctx context.Context, tx *sql.Tx) error {
		// NULL for a question asked mid-build; otherwise a position shared with the story's scenarios.
		_, err := tx.ExecContext(ctx, `ALTER TABLE questions ADD COLUMN position INTEGER;`)
		return err
	}},
	{5, "replacing and retiring scenarios", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			ALTER TABLE scenarios ADD COLUMN replaced_by TEXT REFERENCES scenarios(id);
			ALTER TABLE scenarios ADD COLUMN retired_by TEXT REFERENCES stories(id);
			ALTER TABLE scenarios ADD COLUMN change_reason TEXT NOT NULL DEFAULT '';
			ALTER TABLE scenarios ADD COLUMN changed_at TEXT;`)
		return err
	}},
	{6, "removing a retired scenario's behaviour is work, closed when its test is gone", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE scenarios ADD COLUMN removed_at TEXT;`)
		return err
	}},
	{7, "accepting a finished story", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			ALTER TABLE stories ADD COLUMN accepted_at TEXT;
			ALTER TABLE stories ADD COLUMN accept_note TEXT NOT NULL DEFAULT '';`)
		return err
	}},
	{8, "visible corrections to a question's answers and note", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE question_corrections (
				question_id TEXT NOT NULL REFERENCES questions(id),
				target      TEXT NOT NULL, -- "round N" or "note"
				was         TEXT NOT NULL,
				reason      TEXT NOT NULL,
				created_at  TEXT NOT NULL
			);`)
		return err
	}},
	{9, "when next hands a scenario to the builder", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE scenarios ADD COLUMN handed_out_at TEXT;`)
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
	// An older aat would misread a newer schema (for example, handing out
	// retired scenarios), so it refuses instead.
	if latest := migrations[len(migrations)-1].version; current > latest {
		return fmt.Errorf("this database is at schema version %d, but this aat only knows up to %d; upgrade aat", current, latest)
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

func (s *Store) GetFeature(ctx context.Context, id string) (Feature, error) {
	f := Feature{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT title, description, created_at FROM features WHERE id = ?", id).
		Scan(&f.Title, &f.Description, &f.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return f, notFoundErr("no feature %q", id)
	}
	return f, err
}

func (s *Store) featureRef(ctx context.Context, id string) (FeatureRef, error) {
	f, err := s.GetFeature(ctx, id)
	return FeatureRef{ID: f.ID, Title: f.Title}, err
}

// featureOrder selects a feature's scenarios in the order they read: its
// stories in the order they were added, each story's scenarios in order.
const featureOrder = `story_id IN (SELECT id FROM stories WHERE feature_id = ?)
	ORDER BY (SELECT created_at FROM stories WHERE stories.id = scenarios.story_id),
		(SELECT rowid FROM stories WHERE stories.id = scenarios.story_id), position`

// CurrentScenarios returns the scenarios that make up a feature's spec now,
// leaving out replaced and retired ones.
func (s *Store) CurrentScenarios(ctx context.Context, featureID string) ([]Scenario, error) {
	return s.loadScenarios(ctx, "WHERE "+current+" AND "+featureOrder, featureID)
}

// RetiredScenarios returns a feature's retired scenarios, in the same order.
func (s *Store) RetiredScenarios(ctx context.Context, featureID string) ([]Scenario, error) {
	return s.loadScenarios(ctx, "WHERE retired_by IS NOT NULL AND "+featureOrder, featureID)
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
	st := Story{ID: id, Feature: ref, Title: title, Description: description, CreatedAt: now(), Steps: []Step{}, Questions: []Question{}}
	_, err = s.db.ExecContext(ctx, "INSERT INTO stories (id, feature_id, title, description, created_at) VALUES (?, ?, ?, ?, ?)",
		st.ID, featureID, st.Title, st.Description, st.CreatedAt)
	return st, err
}

// GetStory returns a story with its feature, and its scenarios and planned
// check-ins in one list, in order.
func (s *Store) GetStory(ctx context.Context, id string) (Story, error) {
	st := Story{ID: id}
	err := s.db.QueryRowContext(ctx, `
		SELECT st.title, st.description, st.created_at, f.id, f.title, COALESCE(st.accepted_at, ''), st.accept_note
		FROM stories st JOIN features f ON f.id = st.feature_id
		WHERE st.id = ?`, id).Scan(&st.Title, &st.Description, &st.CreatedAt, &st.Feature.ID, &st.Feature.Title, &st.AcceptedAt, &st.AcceptNote)
	if errors.Is(err, sql.ErrNoRows) {
		return st, notFoundErr("no story %q", id)
	}
	if err != nil {
		return st, err
	}
	scs, err := s.loadScenarios(ctx, "WHERE story_id = ? ORDER BY position", id)
	if err != nil {
		return st, err
	}
	planned, err := s.loadQuestions(ctx, "WHERE story_id = ? AND position IS NOT NULL ORDER BY position", id)
	if err != nil {
		return st, err
	}
	st.Steps = make([]Step, 0, len(scs)+len(planned))
	for i := range scs {
		st.Steps = append(st.Steps, Step{Scenario: &scs[i]})
	}
	for i := range planned {
		st.Steps = append(st.Steps, Step{Question: &planned[i]})
	}
	sort.SliceStable(st.Steps, func(i, j int) bool { return st.Steps[i].Position() < st.Steps[j].Position() })
	if st.Retires, err = s.loadScenarios(ctx, "WHERE retired_by = ? ORDER BY changed_at, rowid", id); err != nil {
		return st, err
	}
	st.Questions, err = s.loadQuestions(ctx, "WHERE story_id = ? AND resolved_at IS NULL AND position IS NULL ORDER BY created_at, rowid", id)
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

// requireOpenStory is requireStory for adding to a story's work: an
// accepted story takes nothing more, and later changes need a new story.
func (s *Store) requireOpenStory(ctx context.Context, id string) error {
	var acceptedAt sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT accepted_at FROM stories WHERE id = ?", id).Scan(&acceptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return notFoundErr("no story %q", id)
	}
	if err != nil {
		return err
	}
	if acceptedAt.Valid {
		return conflictErr("story %q was accepted on %s; make later changes in a new story", id, acceptedAt.String)
	}
	return nil
}

// AcceptStory records that the user reviewed a finished story and accepted
// it. A story is finished when next has nothing left to hand out.
func (s *Store) AcceptStory(ctx context.Context, id, note string) (Story, error) {
	if err := s.requireOpenStory(ctx, id); err != nil {
		return Story{}, err
	}
	sc, q, err := s.Next(ctx, id)
	if err != nil {
		return Story{}, err
	}
	switch {
	case q != nil:
		return Story{}, conflictErr("story %q isn't finished: question %s is still open", id, q.ID)
	case sc != nil:
		return Story{}, conflictErr("story %q isn't finished: scenario %s is still to do", id, sc.ID)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE stories SET accepted_at = ?, accept_note = ? WHERE id = ?", now(), note, id); err != nil {
		return Story{}, err
	}
	return s.GetStory(ctx, id)
}

// AddScenarios adds scenarios to a story, all or nothing: at the end, or in
// front of the draft step before when it is set.
func (s *Store) AddScenarios(ctx context.Context, storyID string, parsed []ParsedScenario, before string) ([]Scenario, error) {
	if err := s.requireOpenStory(ctx, storyID); err != nil {
		return nil, err
	}
	at := 0
	if before != "" {
		var err error
		if at, err = s.placeBefore(ctx, storyID, before); err != nil {
			return nil, err
		}
	}
	var added []Scenario
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		added, err = insertScenarios(ctx, tx, storyID, parsed, "", at)
		return err
	})
	return added, err
}

// insertScenarios adds scenarios to a story inside tx: at the end when at is
// 0, otherwise at that place in the story's order, moving later items along.
// questionID, when set, records the question they came out of.
func insertScenarios(ctx context.Context, tx *sql.Tx, storyID string, parsed []ParsedScenario, questionID string, at int) ([]Scenario, error) {
	pos := at
	if at == 0 {
		var err error
		if pos, err = nextPosition(ctx, tx, storyID); err != nil {
			return nil, err
		}
	} else if err := makeRoom(ctx, tx, storyID, at, len(parsed)); err != nil {
		return nil, err
	}
	var qid any
	if questionID != "" {
		qid = questionID
	}
	var added []Scenario
	for _, p := range parsed {
		id, err := newID(scenarioIDLen)
		if err != nil {
			return nil, err
		}
		sc := Scenario{ID: id, StoryID: storyID, Title: p.Title, Description: p.Description, Lines: p.Lines,
			Position: pos, Status: "open", QuestionID: questionID, CreatedAt: now()}
		pos++
		if _, err := tx.ExecContext(ctx, "INSERT INTO scenarios (id, story_id, position, title, description, question_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			sc.ID, storyID, sc.Position, sc.Title, sc.Description, qid, sc.CreatedAt); err != nil {
			return nil, err
		}
		if err := insertLines(ctx, tx, sc.ID, p.Lines); err != nil {
			return nil, err
		}
		added = append(added, sc)
	}
	return added, nil
}

func insertLines(ctx context.Context, tx *sql.Tx, scenarioID string, lines []Line) error {
	for i, l := range lines {
		if _, err := tx.ExecContext(ctx, "INSERT INTO scenario_lines (scenario_id, position, keyword, text) VALUES (?, ?, ?, ?)",
			scenarioID, i+1, l.Keyword, l.Text); err != nil {
			return err
		}
	}
	return nil
}

// makeRoom moves everything at or after position at in a story's order along
// by n. Scenarios go via negative numbers, so no two of them ever share a
// position on the way.
func makeRoom(ctx context.Context, tx *sql.Tx, storyID string, at, n int) error {
	if _, err := tx.ExecContext(ctx, "UPDATE scenarios SET position = -(position + ?) WHERE story_id = ? AND position >= ?", n, storyID, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE scenarios SET position = -position WHERE story_id = ? AND position < 0", storyID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE questions SET position = position + ? WHERE story_id = ? AND position >= ?", n, storyID, at)
	return err
}

// nextPosition is the next free place in a story's order, which its
// scenarios and planned questions share.
func nextPosition(ctx context.Context, tx *sql.Tx, storyID string) (int, error) {
	var pos int
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(position), 0) FROM (
			SELECT position FROM scenarios WHERE story_id = ?
			UNION ALL
			SELECT position FROM questions WHERE story_id = ? AND position IS NOT NULL)`, storyID, storyID).Scan(&pos)
	return pos + 1, err
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

// TestRef is the test that proves a scenario: a file relative to the project
// root, and the name of a test in that file.
type TestRef struct {
	File string
	Name string
}

// CloseScenario records the test that proves an open scenario. It also
// returns the ids of other closed scenarios that name the same test.
func (s *Store) CloseScenario(ctx context.Context, id string, test TestRef) ([]string, error) {
	var others []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		others, err = closeScenarioTx(ctx, tx, id, test, now())
		return err
	})
	return others, err
}

// closeScenarioTx is CloseScenario inside tx, closing the scenario at the given time.
func closeScenarioTx(ctx context.Context, tx *sql.Tx, id string, test TestRef, at string) ([]string, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE scenarios SET test_file = ?, test_name = ?, closed_at = ?
		WHERE id = ? AND closed_at IS NULL AND `+current, test.File, test.Name, at, id)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = conflictErr("scenario %q is already closed, replaced or retired", id)
		}
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM scenarios
		WHERE test_file = ? AND test_name = ? AND closed_at IS NOT NULL AND id != ? AND `+current+`
		ORDER BY closed_at, rowid`, test.File, test.Name, id)
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

// loadScenarios runs a scenario query with the given WHERE/ORDER clause and
// loads each one's lines, and what it replaced, if anything.
func (s *Store) loadScenarios(ctx context.Context, clause string, args ...any) ([]Scenario, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, story_id, position, title, description, test_file, test_name, COALESCE(question_id, ''),
			created_at, COALESCE(closed_at, ''), COALESCE(replaced_by, ''), COALESCE(retired_by, ''), change_reason,
			COALESCE(removed_at, ''), COALESCE(handed_out_at, '')
		FROM scenarios `+clause, args...)
	if err != nil {
		return nil, err
	}
	scs := []Scenario{}
	for rows.Next() {
		var sc Scenario
		if err := rows.Scan(&sc.ID, &sc.StoryID, &sc.Position, &sc.Title, &sc.Description, &sc.TestFile, &sc.Test, &sc.QuestionID,
			&sc.CreatedAt, &sc.ClosedAt, &sc.ReplacedBy, &sc.RetiredBy, &sc.Reason,
			&sc.RemovedAt, &sc.HandedOut); err != nil {
			rows.Close()
			return nil, err
		}
		switch {
		case sc.ReplacedBy != "":
			sc.Status = "replaced"
		case sc.RetiredBy != "":
			sc.Status = "retired"
		case sc.ClosedAt != "":
			sc.Status = "closed"
		default:
			sc.Status = "open"
		}
		scs = append(scs, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range scs {
		if scs[i].Lines, err = s.loadLines(ctx, scs[i].ID); err != nil {
			return nil, err
		}
		if scs[i].Replaces, err = s.loadReplaced(ctx, scs[i].ID); err != nil {
			return nil, err
		}
	}
	return scs, nil
}

func (s *Store) loadLines(ctx context.Context, scenarioID string) ([]Line, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT keyword, text FROM scenario_lines WHERE scenario_id = ? ORDER BY position", scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.Keyword, &l.Text); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// loadReplaced returns the scenario that the given one replaced, or nil.
func (s *Store) loadReplaced(ctx context.Context, scenarioID string) (*Replaced, error) {
	r := Replaced{}
	err := s.db.QueryRowContext(ctx, "SELECT id, title, test_file, test_name, change_reason FROM scenarios WHERE replaced_by = ?", scenarioID).
		Scan(&r.ID, &r.Title, &r.TestFile, &r.Test, &r.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Lines, err = s.loadLines(ctx, r.ID); err != nil {
		return nil, err
	}
	// If the replaced scenario was never built, it has no test. The test to
	// change is then the one for the newest built rule further back.
	for at := r.ID; r.Test == ""; {
		err := s.db.QueryRowContext(ctx, "SELECT id, test_file, test_name FROM scenarios WHERE replaced_by = ?", at).
			Scan(&at, &r.TestFile, &r.Test)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return &r, nil
}
