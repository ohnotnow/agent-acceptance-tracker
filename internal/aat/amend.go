package aat

import (
	"context"
	"database/sql"
	"flag"
	"io"
	"strings"
)

// Tidying a story before it is built. A step that is not yet
// built or settled is a draft: it can be edited, moved, or have new work put
// before it. A closed scenario's test was written against its words, and a
// question's answers against its text, so those change only through
// questions and replacements, which keep the history.

// draftScenario refuses a scenario that is no longer a draft.
func draftScenario(sc Scenario) error {
	switch sc.Status {
	case "closed":
		return conflictErr("scenario %q is closed with the test %q in %s, which was written against its words; change it through a question instead (aat question ask)", sc.ID, sc.Test, sc.TestFile)
	case "replaced":
		return conflictErr("scenario %q was replaced by %s", sc.ID, sc.ReplacedBy)
	case "retired":
		return conflictErr("scenario %q is retired (%s)", sc.ID, sc.Reason)
	}
	if sc.HandedOut != "" {
		return conflictErr("scenario %q was handed to the builder by aat next at %s, and the builder may already have a test against its words; change it through a question instead (aat question ask)", sc.ID, sc.HandedOut)
	}
	return nil
}

// MarkHandedOut records that next handed a scenario to the builder, the
// first time only.
func (s *Store) MarkHandedOut(ctx context.Context, sc *Scenario) error {
	if sc.HandedOut != "" {
		return nil
	}
	at := now()
	if _, err := s.db.ExecContext(ctx, "UPDATE scenarios SET handed_out_at = ? WHERE id = ? AND handed_out_at IS NULL", at, sc.ID); err != nil {
		return err
	}
	sc.HandedOut = at
	return nil
}

// draftCheckIn refuses a question that is not an open planned check-in.
func draftCheckIn(q Question) error {
	if q.Position == 0 {
		return validationErr("question %q was asked mid-build, so it has no place in the story's order", q.ID)
	}
	if q.Status != "open" {
		return conflictErr("check-in %q is already settled", q.ID)
	}
	return nil
}

// placeBefore is the position of a draft step (scenario or planned check-in)
// in storyID, for putting work in front of it.
func (s *Store) placeBefore(ctx context.Context, storyID, before string) (int, error) {
	if scs, err := s.loadScenarios(ctx, "WHERE id = ?", before); err != nil {
		return 0, err
	} else if len(scs) == 1 {
		if scs[0].StoryID != storyID {
			return 0, validationErr("scenario %q is in story %s, not %s", before, scs[0].StoryID, storyID)
		}
		return scs[0].Position, draftScenario(scs[0])
	}
	qs, err := s.loadQuestions(ctx, "WHERE id = ?", before)
	if err != nil {
		return 0, err
	}
	if len(qs) == 0 {
		return 0, notFoundErr("no scenario or planned check-in %q", before)
	}
	if qs[0].StoryID != storyID {
		return 0, validationErr("question %q is in story %s, not %s", before, qs[0].StoryID, storyID)
	}
	return qs[0].Position, draftCheckIn(qs[0])
}

// EditScenario rewrites a draft scenario in place, keeping its id and place.
func (s *Store) EditScenario(ctx context.Context, id string, p ParsedScenario) (Scenario, error) {
	sc, err := s.GetScenario(ctx, id)
	if err != nil {
		return sc, err
	}
	if err := draftScenario(sc); err != nil {
		return sc, err
	}
	if err := s.requireOpenStory(ctx, sc.StoryID); err != nil {
		return sc, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE scenarios SET title = ?, description = ? WHERE id = ?", p.Title, p.Description, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM scenario_lines WHERE scenario_id = ?", id); err != nil {
			return err
		}
		return insertLines(ctx, tx, id, p.Lines)
	})
	if err != nil {
		return sc, err
	}
	return s.GetScenario(ctx, id)
}

// DeleteScenario removes a draft scenario that was never built. One that
// replaces an earlier rule, or came out of the user's answer to a question,
// is a link in the record, so it stays.
// It leaves a marker, so importing a colleague's copy of the story
// (ant aat-GShrC) doesn't bring the scenario back.
func (s *Store) DeleteScenario(ctx context.Context, id string) error {
	sc, err := s.GetScenario(ctx, id)
	if err != nil {
		return err
	}
	if err := draftScenario(sc); err != nil {
		return err
	}
	if sc.Replaces != nil {
		return conflictErr("scenario %q replaces %s, which would lose the rule that replaced it; edit it instead", id, sc.Replaces.ID)
	}
	if sc.QuestionID != "" {
		return conflictErr("scenario %q came out of the user's answer to question %s; edit it instead, or raise a question", id, sc.QuestionID)
	}
	if err := s.requireOpenStory(ctx, sc.StoryID); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM scenario_lines WHERE scenario_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM scenarios WHERE id = ?", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO deleted_scenarios (id, story_id, deleted_at) VALUES (?, ?, ?)", id, sc.StoryID, now())
		return err
	})
}

// EditStory changes a story's title or description (whichever is non-nil).
// They guide the builder and no test is written against them, so they can
// change until the story is accepted.
func (s *Store) EditStory(ctx context.Context, id string, title, description *string) (Story, error) {
	if err := s.requireOpenStory(ctx, id); err != nil {
		return Story{}, err
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if title != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE stories SET title = ? WHERE id = ?", *title, id); err != nil {
				return err
			}
		}
		if description != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE stories SET description = ? WHERE id = ?", *description, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Story{}, err
	}
	return s.GetStory(ctx, id)
}

// EditQuestion rewrites an open question's text before anyone has answered it.
func (s *Store) EditQuestion(ctx context.Context, id, text string) (Question, error) {
	q, err := s.openQuestion(ctx, id)
	if err != nil {
		return q, err
	}
	rounds, err := s.loadRounds(ctx, id)
	if err != nil {
		return q, err
	}
	if len(rounds) > 0 {
		return q, conflictErr("question %q has %d round(s) of answers, given to its current text; ask a new question instead", id, len(rounds))
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE questions SET text = ? WHERE id = ?", text, id); err != nil {
		return q, err
	}
	return s.GetQuestion(ctx, id)
}

// MoveScenario puts a draft scenario in front of another draft step.
func (s *Store) MoveScenario(ctx context.Context, id, before string) (Scenario, error) {
	sc, err := s.GetScenario(ctx, id)
	if err != nil {
		return sc, err
	}
	if err := draftScenario(sc); err != nil {
		return sc, err
	}
	if err := s.moveStep(ctx, "scenarios", id, sc.StoryID, before); err != nil {
		return sc, err
	}
	return s.GetScenario(ctx, id)
}

// MoveCheckIn puts an open planned check-in in front of another draft step.
func (s *Store) MoveCheckIn(ctx context.Context, id, before string) (Question, error) {
	qs, err := s.loadQuestions(ctx, "WHERE id = ?", id)
	if err != nil {
		return Question{}, err
	}
	if len(qs) == 0 {
		return Question{}, notFoundErr("no question %q", id)
	}
	if err := draftCheckIn(qs[0]); err != nil {
		return qs[0], err
	}
	if err := s.moveStep(ctx, "questions", id, qs[0].StoryID, before); err != nil {
		return qs[0], err
	}
	return s.GetQuestion(ctx, id)
}

// moveStep parks the step at position 0 (never used), makes room in front of
// before, and puts the step there. The gap it leaves behind is harmless:
// only the order of positions matters.
func (s *Store) moveStep(ctx context.Context, table, id, storyID, before string) error {
	if id == before {
		return validationErr("a step can't move in front of itself")
	}
	if err := s.requireOpenStory(ctx, storyID); err != nil {
		return err
	}
	at, err := s.placeBefore(ctx, storyID, before)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET position = 0 WHERE id = ?", id); err != nil {
			return err
		}
		if err := makeRoom(ctx, tx, storyID, at, 1); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE "+table+" SET position = ? WHERE id = ?", at, id)
		return err
	})
}

// oneScenario reads a --body that must hold exactly one Gherkin scenario.
func oneScenario(name, body string, stdin io.Reader, why string) (ParsedScenario, error) {
	text, err := readText(body, stdin)
	if err != nil {
		return ParsedScenario{}, err
	}
	parsed, err := parseScenarios(text)
	if err != nil {
		return ParsedScenario{}, err
	}
	if len(parsed) != 1 {
		return ParsedScenario{}, validationErr("aat %s: %s, and --body has %d", name, why, len(parsed))
	}
	return parsed[0], nil
}

func scenarioEdit(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat scenario edit <scenario> --body GHERKIN")
	if err != nil {
		return nil, err
	}
	fs := newFlags("scenario edit")
	body := fs.String("body", "", "the scenario's new Gherkin")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *body == "" {
		return nil, usageErr("aat scenario edit: --body is required")
	}
	p, err := oneScenario("scenario edit", *body, stdin, "a scenario is edited into exactly one scenario")
	if err != nil {
		return nil, err
	}
	return store.EditScenario(ctx, id, p)
}

func scenarioDelete(ctx context.Context, store *Store, args []string) (any, error) {
	id, rest, err := leadingID(args, "usage: aat scenario delete <scenario>")
	if err != nil {
		return nil, err
	}
	if err := parseFlags(newFlags("scenario delete"), rest); err != nil {
		return nil, err
	}
	if err := store.DeleteScenario(ctx, id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}

func storyEdit(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat story edit <story> [--title T] [--description D]")
	if err != nil {
		return nil, err
	}
	fs := newFlags("story edit")
	titleFlag := fs.String("title", "", "the story's new title")
	descFlag := fs.String("description", "", "the story's new description")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	var title, description *string
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "title":
			title = titleFlag
		case "description":
			description = descFlag
		}
	})
	if title == nil && description == nil {
		return nil, usageErr("aat story edit: give --title, --description or both")
	}
	if title != nil {
		t := strings.TrimSpace(*title)
		if t == "" {
			return nil, validationErr("aat story edit: the title is empty")
		}
		title = &t
	}
	if description != nil {
		d, err := readText(*description, stdin)
		if err != nil {
			return nil, err
		}
		if d == "" {
			return nil, validationErr("aat story edit: the description is empty")
		}
		description = &d
	}
	st, err := store.EditStory(ctx, id, title, description)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": st.ID, "title": st.Title}, nil
}

func questionEdit(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat question edit <question> --body TEXT")
	if err != nil {
		return nil, err
	}
	fs := newFlags("question edit")
	body := fs.String("body", "", "the question's new text")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *body == "" {
		return nil, usageErr("aat question edit: --body is required")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, validationErr("aat question edit: the question is empty")
	}
	return store.EditQuestion(ctx, id, text)
}

// stepMove handles scenario move and question move.
func stepMove(ctx context.Context, name string, args []string, move func(ctx context.Context, id, before string) (any, error)) (any, error) {
	id, rest, err := leadingID(args, "usage: aat "+name+" <id> --before STEP")
	if err != nil {
		return nil, err
	}
	fs := newFlags(name)
	before := fs.String("before", "", "the step to move in front of")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *before == "" {
		return nil, usageErr("aat %s: --before is required", name)
	}
	return move(ctx, id, *before)
}
