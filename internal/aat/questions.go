package aat

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
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
	Position   int     `json:"position,omitempty"` // a planned check-in's place in the story's order
	Note       string  `json:"note,omitempty"`
	Rounds     []Round `json:"rounds,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ResolvedAt string  `json:"resolved_at,omitempty"`
}

// AskQuestion raises a question mid-build. It holds the story at once.
func (s *Store) AskQuestion(ctx context.Context, storyID, text string) (Question, error) {
	return s.addQuestion(ctx, storyID, text, false)
}

// PlanQuestion places a check-in after everything already in the story's
// order. It holds the story only once next reaches it.
func (s *Store) PlanQuestion(ctx context.Context, storyID, text string) (Question, error) {
	return s.addQuestion(ctx, storyID, text, true)
}

func (s *Store) addQuestion(ctx context.Context, storyID, text string, planned bool) (Question, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return Question{}, err
	}
	id, err := newID(questionIDLen)
	if err != nil {
		return Question{}, err
	}
	q := Question{ID: id, StoryID: storyID, Text: text, Status: "open", CreatedAt: now()}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var pos any // NULL unless planned
		if planned {
			p, err := nextPosition(ctx, tx, storyID)
			if err != nil {
				return err
			}
			q.Position, pos = p, p
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO questions (id, story_id, text, position, created_at) VALUES (?, ?, ?, ?, ?)",
			q.ID, q.StoryID, q.Text, pos, q.CreatedAt)
		return err
	})
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
// scenarios to its story, all or nothing. When test is set, there must be
// exactly one new scenario, and it arrives closed by that test; the ids of
// other closed scenarios naming the same test are returned.
func (s *Store) ResolveQuestion(ctx context.Context, id, note string, parsed []ParsedScenario, test *TestRef) (Question, []Scenario, []string, error) {
	q, err := s.openQuestion(ctx, id)
	if err != nil {
		return q, nil, nil, err
	}
	if test != nil && len(parsed) != 1 {
		return q, nil, nil, fmt.Errorf("a test closes exactly one new scenario, got %d", len(parsed))
	}
	var added []Scenario
	var others []string
	q.Note, q.ResolvedAt, q.Status = note, now(), "resolved"
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE questions SET note = ?, resolved_at = ? WHERE id = ?", q.Note, q.ResolvedAt, id); err != nil {
			return err
		}
		var err error
		if added, err = insertScenarios(ctx, tx, q.StoryID, parsed, id); err != nil || test == nil {
			return err
		}
		if others, err = closeScenarioTx(ctx, tx, added[0].ID, *test, q.ResolvedAt); err != nil {
			return err
		}
		added[0].Status, added[0].TestFile, added[0].Test, added[0].ClosedAt = "closed", test.File, test.Name, q.ResolvedAt
		return nil
	})
	return q, added, others, err
}

// Next returns what the story's builder should do next. A question asked
// mid-build comes first, oldest first. Otherwise it is the first open item in
// the story's order: a scenario or a planned check-in. Both are nil when the
// story has nothing left.
func (s *Store) Next(ctx context.Context, storyID string) (*Scenario, *Question, error) {
	if err := s.requireStory(ctx, storyID); err != nil {
		return nil, nil, err
	}
	asked, err := s.loadQuestions(ctx, "WHERE story_id = ? AND resolved_at IS NULL AND position IS NULL ORDER BY created_at, rowid LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	if len(asked) > 0 {
		return nil, &asked[0], nil
	}
	planned, err := s.loadQuestions(ctx, "WHERE story_id = ? AND resolved_at IS NULL AND position IS NOT NULL ORDER BY position LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	scs, err := s.loadScenarios(ctx, "WHERE story_id = ? AND closed_at IS NULL ORDER BY position LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	if len(planned) > 0 && (len(scs) == 0 || planned[0].Position < scs[0].Position) {
		return nil, &planned[0], nil
	}
	if len(scs) > 0 {
		return &scs[0], nil, nil
	}
	return nil, nil, nil
}

// loadQuestions runs a question query with the given WHERE/ORDER clause, without rounds.
func (s *Store) loadQuestions(ctx context.Context, clause string, args ...any) ([]Question, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, story_id, text, COALESCE(position, 0), note, created_at, COALESCE(resolved_at, '')
		FROM questions `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	qs := []Question{}
	for rows.Next() {
		q := Question{Status: "open"}
		if err := rows.Scan(&q.ID, &q.StoryID, &q.Text, &q.Position, &q.Note, &q.CreatedAt, &q.ResolvedAt); err != nil {
			return nil, err
		}
		if q.ResolvedAt != "" {
			q.Status = "resolved"
		}
		qs = append(qs, q)
	}
	return qs, rows.Err()
}

// questionAdd handles question ask and question plan, which take the same
// flags and differ only in when the question holds the story.
func questionAdd(ctx context.Context, name string, args []string, stdin io.Reader,
	add func(ctx context.Context, storyID, text string) (Question, error)) (any, error) {
	fs := newFlags(name)
	story := fs.String("story", "", "story id")
	body := fs.String("body", "", "the question")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *story == "" || *body == "" {
		return nil, usageErr("aat %s: --story and --body are required", name)
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, validationErr("aat %s: the question is empty", name)
	}
	return add(ctx, *story, text)
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

func questionResolve(ctx context.Context, store *Store, args []string, stdin io.Reader, dir string) (any, error) {
	id, rest, err := leadingID(args, "usage: aat question resolve <question> --note TEXT [--scenarios GHERKIN [--test-file F --test NAME]]")
	if err != nil {
		return nil, err
	}
	fs := newFlags("question resolve")
	noteFlag := fs.String("note", "", "why it was settled this way")
	scenariosFlag := fs.String("scenarios", "", "new Gherkin scenarios, if the answer changed behaviour")
	testFile, testName := testFlags(fs)
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *noteFlag == "" {
		return nil, usageErr("aat question resolve: --note is required")
	}
	if *noteFlag == "-" && *scenariosFlag == "-" {
		return nil, usageErr("aat question resolve: only one of --note and --scenarios can read stdin; use @path for the other")
	}
	withTest := *testFile != "" || *testName != ""
	if withTest && (*testFile == "" || strings.TrimSpace(*testName) == "") {
		return nil, usageErr("aat question resolve: --test-file and --test go together")
	}
	if withTest && *scenariosFlag == "" {
		return nil, usageErr("aat question resolve: --test-file and --test prove a new scenario, so they need --scenarios")
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
	var test *TestRef
	if withTest {
		if len(parsed) != 1 {
			return nil, validationErr("aat question resolve: one test proves one scenario, but --scenarios has %d; resolve without the test, then close each new scenario with aat close", len(parsed))
		}
		ref, err := checkTest(dir, *testFile, *testName)
		if err != nil {
			return nil, err
		}
		test = &ref
	}
	q, added, others, err := store.ResolveQuestion(ctx, id, note, parsed, test)
	if err != nil {
		return nil, err
	}
	refs := []map[string]string{}
	for _, sc := range added {
		ref := map[string]string{"id": sc.ID, "title": sc.Title, "status": sc.Status}
		if sc.TestFile != "" {
			ref["test_file"], ref["test"] = sc.TestFile, sc.Test
		}
		refs = append(refs, ref)
	}
	out := map[string]any{"id": q.ID, "status": q.Status, "note": q.Note, "scenarios": refs}
	if test != nil {
		out["check"] = testCheck
	}
	if len(others) > 0 {
		out["warnings"] = sameTestWarnings(others)
	}
	return out, nil
}

// leadingID splits "<id> --flags..." for commands that act on one record.
func leadingID(args []string, usage string) (string, []string, error) {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return "", nil, usageErr("%s", usage)
	}
	return args[0], args[1:], nil
}
