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
	ID          string       `json:"id"`
	StoryID     string       `json:"story_id"`
	Text        string       `json:"text"`
	Status      string       `json:"status"`
	Position    int          `json:"position,omitempty"` // a planned check-in's place in the story's order
	Note        string       `json:"note,omitempty"`
	Rounds      []Round      `json:"rounds,omitempty"`
	Corrections []Correction `json:"corrections,omitempty"`
	CreatedAt   string       `json:"created_at"`
	ResolvedAt  string       `json:"resolved_at,omitempty"`
}

// Correction is a visible change to a recorded answer or resolve note: what
// it said before, why it was changed, and when.
type Correction struct {
	Target    string `json:"target"` // "round N" or "note"
	Was       string `json:"was"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// AskQuestion raises a question mid-build. It holds the story at once.
func (s *Store) AskQuestion(ctx context.Context, storyID, text string) (Question, error) {
	return s.addQuestion(ctx, storyID, text, false, "")
}

// PlanQuestion places a check-in after everything already in the story's
// order, or in front of the draft step before when it is set. It holds the
// story only once next reaches it.
func (s *Store) PlanQuestion(ctx context.Context, storyID, text, before string) (Question, error) {
	return s.addQuestion(ctx, storyID, text, true, before)
}

func (s *Store) addQuestion(ctx context.Context, storyID, text string, planned bool, before string) (Question, error) {
	if err := s.requireOpenStory(ctx, storyID); err != nil {
		return Question{}, err
	}
	at := 0
	if before != "" {
		var err error
		if at, err = s.placeBefore(ctx, storyID, before); err != nil {
			return Question{}, err
		}
	}
	id, err := newID(questionIDLen)
	if err != nil {
		return Question{}, err
	}
	q := Question{ID: id, StoryID: storyID, Text: text, Status: "open", CreatedAt: now()}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var pos any // NULL unless planned
		if planned {
			p := at
			if at == 0 {
				var err error
				if p, err = nextPosition(ctx, tx, storyID); err != nil {
					return err
				}
			} else if err := makeRoom(ctx, tx, storyID, at, 1); err != nil {
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
	if q.Rounds, err = s.loadRounds(ctx, id); err != nil {
		return q, err
	}
	q.Corrections, err = s.loadCorrections(ctx, id)
	return q, err
}

// StoryQuestions returns all of a story's questions, open and resolved,
// oldest first, with their rounds.
func (s *Store) StoryQuestions(ctx context.Context, storyID string) ([]Question, error) {
	qs, err := s.loadQuestions(ctx, "WHERE story_id = ? ORDER BY created_at, rowid", storyID)
	if err != nil {
		return nil, err
	}
	for i := range qs {
		if qs[i].Rounds, err = s.loadRounds(ctx, qs[i].ID); err != nil {
			return nil, err
		}
		if qs[i].Corrections, err = s.loadCorrections(ctx, qs[i].ID); err != nil {
			return nil, err
		}
	}
	return qs, nil
}

func (s *Store) loadCorrections(ctx context.Context, questionID string) ([]Correction, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT target, was, reason, created_at FROM question_corrections WHERE question_id = ? ORDER BY created_at, rowid", questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cs []Correction
	for rows.Next() {
		var c Correction
		if err := rows.Scan(&c.Target, &c.Was, &c.Reason, &c.CreatedAt); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

// CorrectAnswer replaces the text of one recorded answer (round counts from
// 1), keeping what it said before and why it changed.
func (s *Store) CorrectAnswer(ctx context.Context, id string, round int, text, reason string) (Question, error) {
	q, err := s.GetQuestion(ctx, id)
	if err != nil {
		return q, err
	}
	if round < 1 || round > len(q.Rounds) {
		return q, notFoundErr("question %q has no round %d", id, round)
	}
	return s.correct(ctx, q, fmt.Sprintf("round %d", round), q.Rounds[round-1].Answer, text, reason,
		"UPDATE question_rounds SET answer = ? WHERE question_id = ? AND position = ?", text, id, round)
}

// CorrectNote replaces a resolved question's note, keeping what it said
// before and why it changed.
func (s *Store) CorrectNote(ctx context.Context, id, text, reason string) (Question, error) {
	q, err := s.GetQuestion(ctx, id)
	if err != nil {
		return q, err
	}
	if q.Status != "resolved" {
		return q, conflictErr("question %q is still open, so it has no resolve note to correct", id)
	}
	return s.correct(ctx, q, "note", q.Note, text, reason, "UPDATE questions SET note = ? WHERE id = ?", text, id)
}

// correct records a correction and applies the update, together.
func (s *Store) correct(ctx context.Context, q Question, target, was, text, reason, update string, args ...any) (Question, error) {
	if text == was {
		return q, validationErr("%s of question %q already says that", target, q.ID)
	}
	if err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO question_corrections (question_id, target, was, reason, created_at) VALUES (?, ?, ?, ?, ?)",
			q.ID, target, was, reason, now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, update, args...)
		return err
	}); err != nil {
		return q, err
	}
	return s.GetQuestion(ctx, q.ID)
}

func (s *Store) loadRounds(ctx context.Context, questionID string) ([]Round, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT answer, created_at FROM question_rounds WHERE question_id = ? ORDER BY position", questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rounds := []Round{}
	for rows.Next() {
		var r Round
		if err := rows.Scan(&r.Answer, &r.CreatedAt); err != nil {
			return nil, err
		}
		rounds = append(rounds, r)
	}
	return rounds, rows.Err()
}

// Resolution is how a question is settled: a note of what was decided and
// why, and any new scenarios. Test closes the single new scenario. Replaces
// names a current scenario that the single new scenario replaces; the note
// is the reason.
type Resolution struct {
	Note      string
	Scenarios []ParsedScenario
	Test      *TestRef
	Replaces  string
}

// ResolveQuestion closes an open question and appends any new scenarios to
// its story, all or nothing. It returns the ids of other closed scenarios
// that name the same test, if a test was given.
func (s *Store) ResolveQuestion(ctx context.Context, id string, r Resolution) (Question, []Scenario, []string, error) {
	q, err := s.openQuestion(ctx, id)
	if err != nil {
		return q, nil, nil, err
	}
	if (r.Test != nil || r.Replaces != "") && len(r.Scenarios) != 1 {
		return q, nil, nil, fmt.Errorf("a test or a replacement needs exactly one new scenario, got %d", len(r.Scenarios))
	}
	at := 0 // where the new scenarios go: the end of the story, unless replacing within it
	if r.Replaces != "" {
		old, err := s.changeable(ctx, r.Replaces, q.StoryID)
		if err != nil {
			return q, nil, nil, err
		}
		at = placeOf(old, q.StoryID)
	}
	var added []Scenario
	var others []string
	q.Note, q.ResolvedAt, q.Status = r.Note, now(), "resolved"
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE questions SET note = ?, resolved_at = ? WHERE id = ? AND resolved_at IS NULL", q.Note, q.ResolvedAt, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = conflictErr("question %q is already resolved", id)
			}
			return err
		}
		if added, err = insertScenarios(ctx, tx, q.StoryID, r.Scenarios, id, at); err != nil {
			return err
		}
		if r.Replaces != "" {
			if err := markChanged(ctx, tx, "replaced_by", r.Replaces, added[0].ID, r.Note, q.ResolvedAt); err != nil {
				return err
			}
		}
		if r.Test == nil {
			return nil
		}
		if others, err = closeScenarioTx(ctx, tx, added[0].ID, *r.Test, q.ResolvedAt); err != nil {
			return err
		}
		added[0].Status, added[0].TestFile, added[0].Test, added[0].ClosedAt = "closed", r.Test.File, r.Test.Name, q.ResolvedAt
		return nil
	})
	return q, added, others, err
}

// Next returns what the story's builder should do next. A question asked
// mid-build comes first, oldest first. Then a scenario the story retired
// whose removal isn't closed yet, so old behaviour goes before new is built;
// it comes back as a scenario with status "retired". Otherwise it is the
// first open item in the story's order: a scenario or a planned check-in.
// Both are nil when the story has nothing left.
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
	retiring, err := s.loadScenarios(ctx, "WHERE retired_by = ? AND removed_at IS NULL ORDER BY changed_at, rowid LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	if len(retiring) > 0 {
		return &retiring[0], nil, nil
	}
	planned, err := s.loadQuestions(ctx, "WHERE story_id = ? AND resolved_at IS NULL AND position IS NOT NULL ORDER BY position LIMIT 1", storyID)
	if err != nil {
		return nil, nil, err
	}
	scs, err := s.loadScenarios(ctx, "WHERE story_id = ? AND closed_at IS NULL AND "+current+" ORDER BY position LIMIT 1", storyID)
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

// questionAdd handles question ask and question plan, which differ in when
// the question holds the story. Only a planned check-in has a place in the
// story's order, so only plan takes --before.
func questionAdd(ctx context.Context, store *Store, name string, args []string, stdin io.Reader, planned bool) (any, error) {
	fs := newFlags(name)
	story := fs.String("story", "", "story id")
	body := fs.String("body", "", "the question")
	before := new(string)
	if planned {
		before = fs.String("before", "", "the step to put the check-in in front of")
	}
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
	if planned {
		return store.PlanQuestion(ctx, *story, text, *before)
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

func questionResolve(ctx context.Context, store *Store, args []string, stdin io.Reader, dir string) (any, error) {
	id, rest, err := leadingID(args, "usage: aat question resolve <question> --note TEXT [--scenarios GHERKIN [--replaces SCENARIO] [--test-file F --test NAME]]")
	if err != nil {
		return nil, err
	}
	fs := newFlags("question resolve")
	noteFlag := fs.String("note", "", "why it was settled this way")
	scenariosFlag := fs.String("scenarios", "", "new Gherkin scenarios, if the answer changed behaviour")
	replaces := fs.String("replaces", "", "a current scenario that the one new scenario replaces")
	testFile, testName := testFlags(fs)
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *replaces != "" && *scenariosFlag == "" {
		return nil, usageErr("aat question resolve: --replaces needs --scenarios, holding the one scenario that replaces it")
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
	if *replaces != "" && len(parsed) != 1 {
		return nil, validationErr("aat question resolve: a scenario is replaced by exactly one new scenario, but --scenarios has %d", len(parsed))
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
	q, added, others, err := store.ResolveQuestion(ctx, id, Resolution{Note: note, Scenarios: parsed, Test: test, Replaces: *replaces})
	if err != nil {
		return nil, err
	}
	var warnings []string
	if len(others) > 0 {
		warnings = sameTestWarnings(others)
	}
	if test != nil && *replaces != "" {
		sc, err := store.GetScenario(ctx, added[0].ID)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, replacedTestWarnings(dir, sc.Replaces, *test)...)
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
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

func questionCorrect(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	const usage = "usage: aat question correct <question> (--round N | --resolve-note) --body TEXT --reason TEXT"
	id, rest, err := leadingID(args, usage)
	if err != nil {
		return nil, err
	}
	fs := newFlags("question correct")
	round := fs.Int("round", 0, "the answer to correct, counting from 1")
	resolveNote := fs.Bool("resolve-note", false, "correct the resolve note instead of an answer")
	body := fs.String("body", "", "the corrected text")
	reasonFlag := fs.String("reason", "", "why it is being corrected")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if (*round > 0) == *resolveNote {
		return nil, usageErr("aat question correct: give one of --round N or --resolve-note\n%s", usage)
	}
	if *body == "" || *reasonFlag == "" {
		return nil, usageErr("aat question correct: --body and --reason are required\n%s", usage)
	}
	if *body == "-" && *reasonFlag == "-" {
		return nil, usageErr("aat question correct: only one of --body and --reason can read stdin; use @path for the other")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	reason, err := readText(*reasonFlag, stdin)
	if err != nil {
		return nil, err
	}
	if text == "" || reason == "" {
		return nil, validationErr("aat question correct: the corrected text and the reason can't be empty")
	}
	if *resolveNote {
		return store.CorrectNote(ctx, id, text, reason)
	}
	return store.CorrectAnswer(ctx, id, *round, text, reason)
}

// leadingID splits "<id> --flags..." for commands that act on one record.
func leadingID(args []string, usage string) (string, []string, error) {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return "", nil, usageErr("%s", usage)
	}
	return args[0], args[1:], nil
}
