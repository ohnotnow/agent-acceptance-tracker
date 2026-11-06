package aat

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"slices"
)

// Signing off a story (ait aat-L3pHO). The writing session stores a story in
// aat as soon as it drafts it, before the user has seen it. A story is a
// draft until the user says yes and the session records it with
// `story agree`; next refuses a draft story, so nobody builds what the user
// hasn't agreed. (A draft story is not the same as a draft step: an
// unbuilt scenario stays editable in an agreed story, as ant aat-bREz9 says.)

// The four states a story can be in, in the order a story goes through them.
var storyStates = []string{"draft", "building", "built", "accepted"}

// migrationNote marks a sign-off that the database upgrade inferred, rather
// than one the user gave.
const migrationNote = "Set by the migration that added sign-off: this story had already started building, so it was taken as agreed."

// backfillAgreed marks as agreed every unsigned story that has already
// started building: accepted, or with a scenario handed out, closed or
// removed. Its sign-off time is the earliest of those. Run by the migration,
// and by import for files from an aat that had no sign-off.
func backfillAgreed(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE stories SET agree_note = ?, agreed_at = COALESCE(
			(SELECT MIN(t) FROM (
				SELECT handed_out_at AS t FROM scenarios WHERE story_id = stories.id
				UNION ALL SELECT closed_at FROM scenarios WHERE story_id = stories.id
				UNION ALL SELECT removed_at FROM scenarios WHERE retired_by = stories.id)),
			accepted_at)
		WHERE agreed_at IS NULL AND (accepted_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM scenarios
			WHERE (story_id = stories.id AND (handed_out_at IS NOT NULL OR closed_at IS NOT NULL))
				OR (retired_by = stories.id AND removed_at IS NOT NULL)))`, migrationNote)
	return err
}

// storyState works out which of the four states a story is in. It uses
// Next's logic but never hands anything out.
func (s *Store) storyState(ctx context.Context, st Story) (string, *Question, error) {
	switch {
	case st.AcceptedAt != "":
		return "accepted", nil, nil
	case st.AgreedAt == "":
		return "draft", nil, nil
	}
	sc, q, err := s.Next(ctx, st.ID)
	switch {
	case err != nil:
		return "", nil, err
	case q != nil:
		return "building", q, nil
	case sc != nil:
		return "building", nil, nil
	}
	return "built", nil, nil
}

// requireAgreed refuses a draft story, for next.
func (s *Store) requireAgreed(ctx context.Context, id string) error {
	var agreedAt sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT agreed_at FROM stories WHERE id = ?", id).Scan(&agreedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return notFoundErr("no story %q", id)
	}
	if err != nil {
		return err
	}
	if !agreedAt.Valid {
		return conflictErr("story %q is still a draft: the user hasn't signed it off. Once they have said yes, record it with aat story agree %s --note \"<their words>\"", id, id)
	}
	return nil
}

// AgreeStory records that the user signed a draft story off for building.
func (s *Store) AgreeStory(ctx context.Context, id, note string) (Story, error) {
	if err := s.requireOpenStory(ctx, id); err != nil {
		return Story{}, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE stories SET agreed_at = ?, agree_note = ? WHERE id = ? AND agreed_at IS NULL", now(), note, id)
	if err != nil {
		return Story{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Story{}, err
	} else if n == 0 {
		return Story{}, conflictErr("story %q is already signed off", id)
	}
	return s.GetStory(ctx, id)
}

func storyAgree(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat story agree <story> --note TEXT")
	if err != nil {
		return nil, err
	}
	fs := newFlags("story agree")
	noteFlag := fs.String("note", "", "what the user said on signing it off")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *noteFlag == "" {
		return nil, usageErr("aat story agree: --note is required")
	}
	note, err := readText(*noteFlag, stdin)
	if err != nil {
		return nil, err
	}
	if note == "" {
		return nil, validationErr("aat story agree: the note is empty")
	}
	st, err := store.AgreeStory(ctx, id, note)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": st.ID, "state": st.State, "agreed_at": st.AgreedAt, "agree_note": st.AgreeNote}, nil
}

// StorySummary is one line of `aat stories`.
type StorySummary struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Feature     FeatureRef `json:"feature"`
	State       string     `json:"state"`
	Progress    Progress   `json:"progress"`
	HeldBy      string     `json:"held_by,omitempty"` // the open question next would hand out, if any
	LastTouched string     `json:"last_touched"`
}

// Progress counts a story's current scenarios (not replaced or retired) and how many are closed.
type Progress struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

// lastTouched is the newest timestamp anywhere in a story's records. The
// times are all UTC RFC 3339, so the largest string is the latest time.
const lastTouched = `(SELECT MAX(t) FROM (
	SELECT st.created_at AS t UNION ALL SELECT st.agreed_at UNION ALL SELECT st.accepted_at
	UNION ALL SELECT created_at FROM scenarios WHERE story_id = st.id
	UNION ALL SELECT closed_at FROM scenarios WHERE story_id = st.id
	UNION ALL SELECT handed_out_at FROM scenarios WHERE story_id = st.id
	UNION ALL SELECT changed_at FROM scenarios WHERE retired_by = st.id OR replaced_by IN (SELECT id FROM scenarios WHERE story_id = st.id)
	UNION ALL SELECT removed_at FROM scenarios WHERE retired_by = st.id
	UNION ALL SELECT created_at FROM questions WHERE story_id = st.id
	UNION ALL SELECT resolved_at FROM questions WHERE story_id = st.id
	UNION ALL SELECT r.created_at FROM question_rounds r JOIN questions q ON q.id = r.question_id WHERE q.story_id = st.id
	UNION ALL SELECT c.created_at FROM question_corrections c JOIN questions q ON q.id = c.question_id WHERE q.story_id = st.id
	UNION ALL SELECT deleted_at FROM deleted_scenarios WHERE story_id = st.id))`

// ListStories returns every story, newest-touched first, each with its state.
func (s *Store) ListStories(ctx context.Context) ([]StorySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT st.id, st.title, f.id, f.title, COALESCE(st.agreed_at, ''), COALESCE(st.accepted_at, ''), `+lastTouched+`,
			(SELECT COUNT(*) FROM scenarios WHERE story_id = st.id AND `+current+` AND closed_at IS NOT NULL),
			(SELECT COUNT(*) FROM scenarios WHERE story_id = st.id AND `+current+`)
		FROM stories st JOIN features f ON f.id = st.feature_id
		ORDER BY 7 DESC, st.rowid DESC`)
	if err != nil {
		return nil, err
	}
	type row struct {
		summary              StorySummary
		agreedAt, acceptedAt string
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.summary.ID, &r.summary.Title, &r.summary.Feature.ID, &r.summary.Feature.Title,
			&r.agreedAt, &r.acceptedAt, &r.summary.LastTouched, &r.summary.Progress.Closed, &r.summary.Progress.Total); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]StorySummary, 0, len(found))
	for _, r := range found {
		state, q, err := s.storyState(ctx, Story{ID: r.summary.ID, AgreedAt: r.agreedAt, AcceptedAt: r.acceptedAt})
		if err != nil {
			return nil, err
		}
		r.summary.State = state
		if q != nil {
			r.summary.HeldBy = q.ID
		}
		out = append(out, r.summary)
	}
	return out, nil
}

func listStories(ctx context.Context, store *Store, args []string) (any, error) {
	fs := newFlags("stories")
	state := fs.String("state", "", "only stories in this state: "+joinStates())
	open := fs.Bool("open", false, "only stories not yet accepted")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *state != "" && *open {
		return nil, usageErr("aat stories: give --state or --open, not both")
	}
	if *state != "" && !slices.Contains(storyStates, *state) {
		return nil, usageErr("aat stories: --state must be one of %s", joinStates())
	}
	all, err := store.ListStories(ctx)
	if err != nil {
		return nil, err
	}
	stories := []StorySummary{}
	for _, st := range all {
		if (*state == "" || st.State == *state) && !(*open && st.State == "accepted") {
			stories = append(stories, st)
		}
	}
	return map[string]any{"stories": stories}, nil
}

func joinStates() string {
	out := ""
	for i, st := range storyStates {
		if i > 0 {
			out += ", "
		}
		out += st
	}
	return out
}

// featureList is `aat feature list`: every feature, with how many of its
// stories are in each state (states with none are left out).
func featureList(ctx context.Context, store *Store) (any, error) {
	features, err := store.ListFeatures(ctx)
	if err != nil {
		return nil, err
	}
	stories, err := store.ListStories(ctx)
	if err != nil {
		return nil, err
	}
	type listed struct {
		Feature
		Stories map[string]int `json:"stories,omitempty"`
	}
	out := make([]listed, len(features))
	for i, f := range features {
		out[i] = listed{Feature: f}
		for _, st := range stories {
			if st.Feature.ID == f.ID {
				if out[i].Stories == nil {
					out[i].Stories = map[string]int{}
				}
				out[i].Stories[st.State]++
			}
		}
	}
	return map[string]any{"features": out}, nil
}
