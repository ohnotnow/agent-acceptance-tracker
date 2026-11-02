package aat

import (
	"context"
	"database/sql"
	"fmt"
	"io"
)

// Changing the spec later (foundation aat-AkRXV, "The spec outlives the
// build"): a story can replace a current scenario with a new one, or retire
// it. Nothing is deleted. The old scenario stays in the record, marked, with
// the reason, so a future agent finds the current rule, the old one, and why.

// changeable checks that a story can replace or retire a scenario: both
// exist, the scenario is still current, and both are on the same feature.
// It returns the scenario.
func (s *Store) changeable(ctx context.Context, scenarioID, storyID string) (Scenario, error) {
	if err := s.requireOpenStory(ctx, storyID); err != nil {
		return Scenario{}, err
	}
	sc, err := s.GetScenario(ctx, scenarioID)
	if err != nil {
		return sc, err
	}
	if sc.Status == "replaced" || sc.Status == "retired" {
		return sc, conflictErr("scenario %q is already %s", scenarioID, sc.Status)
	}
	var same bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT feature_id FROM stories WHERE id = ?) = (SELECT feature_id FROM stories WHERE id = ?)`,
		sc.StoryID, storyID).Scan(&same); err != nil {
		return sc, err
	}
	if !same {
		return sc, validationErr("scenario %q is on a different feature from story %q", scenarioID, storyID)
	}
	return sc, nil
}

// placeOf is where a replacement for old goes in a story: straight after old
// when it is in the same story, so it is built at the point the old one was,
// and otherwise at the end (0).
func placeOf(old Scenario, storyID string) int {
	if old.StoryID == storyID {
		return old.Position + 1
	}
	return 0
}

// markChanged marks a current scenario replaced or retired inside tx.
// column is replaced_by (by = the new scenario) or retired_by (by = the story).
func markChanged(ctx context.Context, tx *sql.Tx, column, id, by, reason, at string) error {
	res, err := tx.ExecContext(ctx, "UPDATE scenarios SET "+column+" = ?, change_reason = ?, changed_at = ? WHERE id = ? AND "+current,
		by, reason, at, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = conflictErr("scenario %q has already been replaced or retired", id)
		}
		return err
	}
	return nil
}

// ReplaceScenario adds one scenario to a story in place of a current one on
// the same feature, and returns the new scenario.
func (s *Store) ReplaceScenario(ctx context.Context, oldID, storyID string, p ParsedScenario, reason string) (Scenario, error) {
	old, err := s.changeable(ctx, oldID, storyID)
	if err != nil {
		return Scenario{}, err
	}
	var replacement string
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		added, err := insertScenarios(ctx, tx, storyID, []ParsedScenario{p}, "", placeOf(old, storyID))
		if err != nil {
			return err
		}
		replacement = added[0].ID
		return markChanged(ctx, tx, "replaced_by", oldID, replacement, reason, now())
	})
	if err != nil {
		return Scenario{}, err
	}
	return s.GetScenario(ctx, replacement)
}

// RetireScenario marks a current scenario retired by a story on the same
// feature. That story's builder removes the behaviour and its test.
func (s *Store) RetireScenario(ctx context.Context, id, storyID, reason string) (Scenario, error) {
	if _, err := s.changeable(ctx, id, storyID); err != nil {
		return Scenario{}, err
	}
	if err := s.inTx(ctx, func(tx *sql.Tx) error {
		return markChanged(ctx, tx, "retired_by", id, storyID, reason, now())
	}); err != nil {
		return Scenario{}, err
	}
	return s.GetScenario(ctx, id)
}

// RemoveRetired records that a retired scenario's behaviour and test have
// been removed, and returns when.
func (s *Store) RemoveRetired(ctx context.Context, id string) (string, error) {
	at := now()
	res, err := s.db.ExecContext(ctx, "UPDATE scenarios SET removed_at = ? WHERE id = ? AND retired_by IS NOT NULL AND removed_at IS NULL", at, id)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = conflictErr("scenario %q isn't a retirement waiting to be removed", id)
		}
		return "", err
	}
	return at, nil
}

// FeatureView is a feature's spec as it stands, plus what it has retired.
// Replaced scenarios appear under the scenario that replaced them.
type FeatureView struct {
	Feature
	Scenarios []Scenario `json:"scenarios"`
	Retired   []Scenario `json:"retired"`
}

func featureShow(ctx context.Context, store *Store, args []string) (any, error) {
	if len(args) != 1 {
		return nil, usageErr("usage: aat feature show <feature>")
	}
	f, err := store.GetFeature(ctx, args[0])
	if err != nil {
		return nil, err
	}
	view := FeatureView{Feature: f}
	if view.Scenarios, err = store.CurrentScenarios(ctx, f.ID); err != nil {
		return nil, err
	}
	view.Retired, err = store.RetiredScenarios(ctx, f.ID)
	return view, err
}

func scenarioReplace(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat scenario replace <scenario> --story S --reason R --body GHERKIN")
	if err != nil {
		return nil, err
	}
	fs := newFlags("scenario replace")
	story := fs.String("story", "", "the story making the change")
	reasonFlag := fs.String("reason", "", "why the old scenario is replaced")
	body := fs.String("body", "", "the one new Gherkin scenario")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *story == "" || *reasonFlag == "" || *body == "" {
		return nil, usageErr("aat scenario replace: --story, --reason and --body are required")
	}
	if *reasonFlag == "-" && *body == "-" {
		return nil, usageErr("aat scenario replace: only one of --reason and --body can read stdin; use @path for the other")
	}
	reason, err := readText(*reasonFlag, stdin)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return nil, validationErr("aat scenario replace: the reason is empty")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	parsed, err := parseScenarios(text)
	if err != nil {
		return nil, err
	}
	if len(parsed) != 1 {
		return nil, validationErr("aat scenario replace: one scenario is replaced by exactly one new scenario, and --body has %d", len(parsed))
	}
	return store.ReplaceScenario(ctx, id, *story, parsed[0], reason)
}

func scenarioRetire(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat scenario retire <scenario> --story S --reason R")
	if err != nil {
		return nil, err
	}
	fs := newFlags("scenario retire")
	story := fs.String("story", "", "the story making the change")
	reasonFlag := fs.String("reason", "", "why the scenario is retired")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *story == "" || *reasonFlag == "" {
		return nil, usageErr("aat scenario retire: --story and --reason are required")
	}
	reason, err := readText(*reasonFlag, stdin)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return nil, validationErr("aat scenario retire: the reason is empty")
	}
	return store.RetireScenario(ctx, id, *story, reason)
}

// replacedTestWarnings warns when the test for the rule a scenario replaced is
// still in its file: whoever built the replacement should change or remove
// it. It stays quiet when the replacement is closed by that same test (the
// builder changed the old test in place). Like closing, it only looks for the
// name as text.
func replacedTestWarnings(dir string, old *Replaced, closedWith TestRef) []string {
	if old == nil || old.TestFile == "" || (old.TestFile == closedWith.File && old.Test == closedWith.Name) {
		return nil
	}
	if !testStillThere(dir, TestRef{File: old.TestFile, Name: old.Test}) {
		return nil
	}
	return []string{fmt.Sprintf("the test for the rule this replaces, %q, is still in %s; change or remove it", old.Test, old.TestFile)}
}
