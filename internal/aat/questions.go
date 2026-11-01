package aat

import (
	"context"
	"database/sql"
	"io"
)

// Questions are the proof-of-concept cut described in ant aat-cwm6b: a gap
// found mid-build holds the story's queue, collects the user's answers as
// rounds, and on resolving merges only the outcome (a note, plus any new
// scenarios) into the story.

type Round struct {
	Answer    string `json:"answer"`
	CreatedAt string `json:"created_at"`
}

type Question struct {
	ID         string  `json:"id"`
	StoryID    string  `json:"story_id"`
	Text       string  `json:"text"`
	Status     string  `json:"status"`
	Note       string  `json:"note,omitempty"`
	Rounds     []Round `json:"rounds,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ResolvedAt string  `json:"resolved_at,omitempty"`
}

func (s *Store) AskQuestion(ctx context.Context, storyID, text string) (Question, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return Question{}, err
	}
	id, err := newID(questionIDLen)
	if err != nil {
		return Question{}, err
	}
	q := Question{ID: id, StoryID: storyID, Text: text, Status: "open", CreatedAt: now()}
	_, err = s.db.ExecContext(ctx, "INSERT INTO questions (id, story_id, text, created_at) VALUES (?, ?, ?, ?)",
		q.ID, q.StoryID, q.Text, q.CreatedAt)
	return q, err
}

// openQuestion returns the question if it exists and is still open.
func (s *Store) openQuestion(ctx context.Context, id string) (Question, error) {
	qs, err := s.loadQuestions(ctx, "WHERE id = ?", id)
	if err != nil {
		return Question{}, err
	}
	if len(qs) == 0 {
		return Question{}, notFoundErr("no question %q", id)
	}
	if qs[0].Status != "open" {
		return Question{}, conflictErr("question %q is already resolved", id)
	}
	return qs[0], nil
}

// AnswerQuestion records one round of the user's answer and returns the round count.
func (s *Store) AnswerQuestion(ctx context.Context, id, answer string) (int, error) {
	if _, err := s.openQuestion(ctx, id); err != nil {
		return 0, err
	}
	var rounds int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM question_rounds WHERE question_id = ?", id).Scan(&rounds); err != nil {
			return err
		}
		rounds++
		_, err := tx.ExecContext(ctx, "INSERT INTO question_rounds (question_id, position, answer, created_at) VALUES (?, ?, ?, ?)",
			id, rounds, answer, now())
		return err
	})
	return rounds, err
}

// GetQuestion returns a question with all its rounds.
func (s *Store) GetQuestion(ctx context.Context, id string) (Question, error) {
	qs, err := s.loadQuestions(ctx, "WHERE id = ?", id)
	if err != nil {
		return Question{}, err
	}
	if len(qs) == 0 {
		return Question{}, notFoundErr("no question %q", id)
	}
	q := qs[0]
	rows, err := s.db.QueryContext(ctx, "SELECT answer, created_at FROM question_rounds WHERE question_id = ? ORDER BY position", id)
	if err != nil {
		return q, err
	}
	defer rows.Close()
	q.Rounds = []Round{}
	for rows.Next() {
		var r Round
		if err := rows.Scan(&r.Answer, &r.CreatedAt); err != nil {
			return q, err
		}
		q.Rounds = append(q.Rounds, r)
	}
	return q, rows.Err()
}

// ResolveQuestion closes an open question with a note and appends any new
// scenarios to its story, all or nothing.
func (s *Store) ResolveQuestion(ctx context.Context, id, note string, parsed []ParsedScenario) (Question, []Scenario, error) {
	q, err := s.openQuestion(ctx, id)
	if err != nil {
		return q, nil, err
	}
	var added []Scenario
	q.Note, q.ResolvedAt, q.Status = note, now(), "resolved"
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE questions SET note = ?, resolved_at = ? WHERE id = ?", q.Note, q.ResolvedAt, id); err != nil {
			return err
		}
		added, err = insertScenarios(ctx, tx, q.StoryID, parsed, id)
		return err
	})
	return q, added, err
}

// Next returns the story's oldest open question if it has one, otherwise its
// first open scenario. Both are nil when the story has nothing left.
func (s *Store) Next(ctx context.Context, storyID string) (*Scenario, *Question, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return nil, nil, err
	}
	qs, err := s.loadQuestions(ctx, "WHERE story_id = ? AND resolved_at IS NULL ORDER BY created_at, rowid LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	if len(qs) > 0 {
		return nil, &qs[0], nil
	}
	scs, err := s.loadScenarios(ctx, "WHERE story_id = ? AND closed_at IS NULL ORDER BY position LIMIT 1", storyID)
	if err != nil || len(scs) == 0 {
		return nil, nil, err
	}
	return &scs[0], nil, nil
}

// loadQuestions runs a question query with the given WHERE/ORDER clause, without rounds.
func (s *Store) loadQuestions(ctx context.Context, clause string, args ...any) ([]Question, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, story_id, text, note, created_at, COALESCE(resolved_at, '')
		FROM questions `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	qs := []Question{}
	for rows.Next() {
		q := Question{Status: "open"}
		if err := rows.Scan(&q.ID, &q.StoryID, &q.Text, &q.Note, &q.CreatedAt, &q.ResolvedAt); err != nil {
			return nil, err
		}
		if q.ResolvedAt != "" {
			q.Status = "resolved"
		}
		qs = append(qs, q)
	}
	return qs, rows.Err()
}

func questionAsk(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	fs := newFlags("question ask")
	story := fs.String("story", "", "story id")
	body := fs.String("body", "", "the question")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *story == "" || *body == "" {
		return nil, usageErr("aat question ask: --story and --body are required")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, validationErr("aat question ask: the question is empty")
	}
	return store.AskQuestion(ctx, *story, text)
}

func questionAnswer(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat question answer <question> --body TEXT")
	if err != nil {
		return nil, err
	}
	fs := newFlags("question answer")
	body := fs.String("body", "", "the user's answer")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *body == "" {
		return nil, usageErr("aat question answer: --body is required")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, validationErr("aat question answer: the answer is empty")
	}
	rounds, err := store.AnswerQuestion(ctx, id, text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "open", "rounds": rounds}, nil
}

func questionResolve(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat question resolve <question> --note TEXT [--scenarios GHERKIN]")
	if err != nil {
		return nil, err
	}
	fs := newFlags("question resolve")
	noteFlag := fs.String("note", "", "why it was settled this way")
	scenariosFlag := fs.String("scenarios", "", "new Gherkin scenarios, if the answer changed behaviour")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *noteFlag == "" {
		return nil, usageErr("aat question resolve: --note is required")
	}
	if *noteFlag == "-" && *scenariosFlag == "-" {
		return nil, usageErr("aat question resolve: only one of --note and --scenarios can read stdin; use @path for the other")
	}
	note, err := readText(*noteFlag, stdin)
	if err != nil {
		return nil, err
	}
	if note == "" {
		return nil, validationErr("aat question resolve: the note is empty")
	}
	var parsed []ParsedScenario
	if *scenariosFlag != "" {
		text, err := readText(*scenariosFlag, stdin)
		if err != nil {
			return nil, err
		}
		if parsed, err = parseScenarios(text); err != nil {
			return nil, err
		}
	}
	q, added, err := store.ResolveQuestion(ctx, id, note, parsed)
	if err != nil {
		return nil, err
	}
	refs := []map[string]string{}
	for _, sc := range added {
		refs = append(refs, map[string]string{"id": sc.ID, "title": sc.Title})
	}
	return map[string]any{"id": q.ID, "status": q.Status, "note": q.Note, "scenarios": refs}, nil
}

// leadingID splits "<id> --flags..." for commands that act on one record.
func leadingID(args []string, usage string) (string, []string, error) {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return "", nil, usageErr("%s", usage)
	}
	return args[0], args[1:], nil
}
