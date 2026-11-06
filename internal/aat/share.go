package aat

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sharing a spec between developers (ant aat-GShrC): `publish --yaml` writes
// everything aat holds about a feature to one YAML file, and `import` merges
// such a file into another developer's database. Records are matched by id
// (random, so two databases never mint the same one). Import adds what is
// missing and takes any change that only moves forward: a field filled in
// once (a test named, a question resolved, a story accepted, a scenario
// replaced or retired) or a list that only grows (answers, corrections).
// Any other difference is a clash: import stops, writes nothing, and lists
// the records, for the people involved to sort out.

// shareFormat is the version of the YAML layout. Import refuses a file from a
// newer one, as OpenStore refuses a newer schema.
// Format 2 added a story's sign-off (agreed_at, agree_note).
const shareFormat = 2

// shareCheck says how each YAML file was checked before it was written.
const shareCheck = "read back with aat import's own reader"

type sharedFile struct {
	Format  int           `yaml:"format"`
	Feature sharedFeature `yaml:"feature"`
	Stories []sharedStory `yaml:"stories"`
}

type sharedFeature struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description,omitempty"`
	CreatedAt   string `yaml:"created_at"`
}

type sharedStory struct {
	ID          string           `yaml:"id"`
	Title       string           `yaml:"title"`
	Description string           `yaml:"description,omitempty"`
	CreatedAt   string           `yaml:"created_at"`
	AgreedAt    string           `yaml:"agreed_at,omitempty"`
	AgreeNote   string           `yaml:"agree_note,omitempty"`
	AcceptedAt  string           `yaml:"accepted_at,omitempty"`
	AcceptNote  string           `yaml:"accept_note,omitempty"`
	Scenarios   []sharedScenario `yaml:"scenarios"`
	Questions   []sharedQuestion `yaml:"questions,omitempty"`
	Deleted     []sharedDeleted  `yaml:"deleted,omitempty"` // draft scenarios deleted before they were built
}

type sharedDeleted struct {
	ID        string `yaml:"id"`
	DeletedAt string `yaml:"deleted_at"`
}

type sharedScenario struct {
	ID          string       `yaml:"id"`
	Position    int          `yaml:"position"` // place in the story's order, shared with planned check-ins
	Title       string       `yaml:"title"`
	Description string       `yaml:"description,omitempty"`
	Lines       []sharedLine `yaml:"lines"`
	CreatedAt   string       `yaml:"created_at"`
	QuestionID  string       `yaml:"question_id,omitempty"`
	HandedOutAt string       `yaml:"handed_out_at,omitempty"`
	TestFile    string       `yaml:"test_file,omitempty"`
	Test        string       `yaml:"test,omitempty"`
	ClosedAt    string       `yaml:"closed_at,omitempty"`
	ReplacedBy  string       `yaml:"replaced_by,omitempty"`
	RetiredBy   string       `yaml:"retired_by,omitempty"`
	Reason      string       `yaml:"reason,omitempty"`
	ChangedAt   string       `yaml:"changed_at,omitempty"`
	RemovedAt   string       `yaml:"removed_at,omitempty"`
}

type sharedLine struct {
	Keyword string `yaml:"keyword"`
	Text    string `yaml:"text"`
}

type sharedQuestion struct {
	ID          string             `yaml:"id"`
	Position    int                `yaml:"position,omitempty"` // a planned check-in's place; absent for one asked mid-build
	Text        string             `yaml:"text"`
	CreatedAt   string             `yaml:"created_at"`
	Rounds      []sharedRound      `yaml:"rounds,omitempty"`
	ResolvedAt  string             `yaml:"resolved_at,omitempty"`
	Note        string             `yaml:"note,omitempty"`
	Corrections []sharedCorrection `yaml:"corrections,omitempty"`
}

type sharedRound struct {
	Answer    string `yaml:"answer"`
	CreatedAt string `yaml:"created_at"`
}

type sharedCorrection struct {
	Target    string `yaml:"target"`
	Was       string `yaml:"was"`
	Reason    string `yaml:"reason"`
	CreatedAt string `yaml:"created_at"`
}

// sharedBy is the first line of every YAML file aat writes, so a file written
// by a person is never overwritten.
func sharedBy(featureID string) string {
	return fmt.Sprintf("# Written by aat publish --yaml from feature %s. Merge it with aat import; edit the spec in aat, not this file.", featureID)
}

// renderShared writes everything the database holds about a feature as YAML,
// and checks that import reads it back exactly.
func renderShared(ctx context.Context, store *Store, featureID string) (string, int, error) {
	f, err := store.sharedFeature(ctx, featureID)
	if err != nil {
		return "", 0, err
	}
	body, err := encodeShared(f)
	if err != nil {
		return "", 0, err
	}
	back, err := decodeShared([]byte(body))
	if err != nil {
		return "", 0, fmt.Errorf("feature %s doesn't read back: %w", featureID, err)
	}
	if again, err := encodeShared(back); err != nil || again != body {
		return "", 0, fmt.Errorf("feature %s doesn't read back as written", featureID)
	}
	return sharedBy(featureID) + "\n" + body, len(f.Stories), nil
}

func encodeShared(f sharedFile) (string, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func decodeShared(b []byte) (sharedFile, error) {
	var f sharedFile
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	return f, nil
}

// sharedFeature reads a feature and everything under it straight from the
// tables, so nothing is left out.
func (s *Store) sharedFeature(ctx context.Context, featureID string) (sharedFile, error) {
	feature, err := s.GetFeature(ctx, featureID)
	if err != nil {
		return sharedFile{}, err
	}
	out := sharedFile{Format: shareFormat, Feature: sharedFeature(feature), Stories: []sharedStory{}}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, description, created_at, COALESCE(agreed_at, ''), agree_note, COALESCE(accepted_at, ''), accept_note
		FROM stories WHERE feature_id = ? ORDER BY created_at, rowid`, featureID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var st sharedStory
		if err := rows.Scan(&st.ID, &st.Title, &st.Description, &st.CreatedAt, &st.AgreedAt, &st.AgreeNote, &st.AcceptedAt, &st.AcceptNote); err != nil {
			rows.Close()
			return out, err
		}
		out.Stories = append(out.Stories, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := range out.Stories {
		st := &out.Stories[i]
		if st.Scenarios, err = s.sharedScenarios(ctx, st.ID); err != nil {
			return out, err
		}
		if st.Questions, err = s.sharedQuestions(ctx, st.ID); err != nil {
			return out, err
		}
		if st.Deleted, err = s.sharedDeleted(ctx, st.ID); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) sharedScenarios(ctx context.Context, storyID string) ([]sharedScenario, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, position, title, description, created_at, COALESCE(question_id, ''), COALESCE(handed_out_at, ''),
			test_file, test_name, COALESCE(closed_at, ''), COALESCE(replaced_by, ''), COALESCE(retired_by, ''),
			change_reason, COALESCE(changed_at, ''), COALESCE(removed_at, '')
		FROM scenarios WHERE story_id = ? ORDER BY position`, storyID)
	if err != nil {
		return nil, err
	}
	scs := []sharedScenario{}
	for rows.Next() {
		var sc sharedScenario
		if err := rows.Scan(&sc.ID, &sc.Position, &sc.Title, &sc.Description, &sc.CreatedAt, &sc.QuestionID, &sc.HandedOutAt,
			&sc.TestFile, &sc.Test, &sc.ClosedAt, &sc.ReplacedBy, &sc.RetiredBy, &sc.Reason, &sc.ChangedAt, &sc.RemovedAt); err != nil {
			rows.Close()
			return nil, err
		}
		scs = append(scs, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range scs {
		lines, err := s.loadLines(ctx, scs[i].ID)
		if err != nil {
			return nil, err
		}
		scs[i].Lines = []sharedLine{}
		for _, l := range lines {
			scs[i].Lines = append(scs[i].Lines, sharedLine(l))
		}
	}
	return scs, nil
}

func (s *Store) sharedQuestions(ctx context.Context, storyID string) ([]sharedQuestion, error) {
	qs, err := s.StoryQuestions(ctx, storyID)
	if err != nil {
		return nil, err
	}
	var out []sharedQuestion
	for _, q := range qs {
		sq := sharedQuestion{ID: q.ID, Position: q.Position, Text: q.Text, CreatedAt: q.CreatedAt, ResolvedAt: q.ResolvedAt, Note: q.Note}
		for _, r := range q.Rounds {
			sq.Rounds = append(sq.Rounds, sharedRound(r))
		}
		for _, c := range q.Corrections {
			sq.Corrections = append(sq.Corrections, sharedCorrection(c))
		}
		out = append(out, sq)
	}
	return out, nil
}

func (s *Store) sharedDeleted(ctx context.Context, storyID string) ([]sharedDeleted, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, deleted_at FROM deleted_scenarios WHERE story_id = ? ORDER BY deleted_at, id", storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sharedDeleted
	for rows.Next() {
		var d sharedDeleted
		if err := rows.Scan(&d.ID, &d.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// importFiles merges one or more YAML files into the database, all or nothing.
func importFiles(ctx context.Context, store *Store, args []string, dir string) (any, error) {
	if len(args) == 0 {
		return nil, usageErr("usage: aat import <file>...")
	}
	files := make([]sharedFile, len(args))
	for i, path := range args {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, notFoundErr("no file %s", args[i])
		}
		if err != nil {
			return nil, err
		}
		if files[i], err = decodeShared(b); err != nil {
			return nil, validationErr("%s isn't a file aat can import: %v", args[i], err)
		}
		if err := checkShared(files[i]); err != nil {
			return nil, validationErr("%s isn't a file aat can import: %v", args[i], err)
		}
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Records refer to each other (a scenario to its replacement), so check
	// those references once everything is in.
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return nil, err
	}
	m := &merger{ctx: ctx, tx: tx}
	results := []map[string]any{}
	for i, f := range files {
		added, updated := m.added, m.updated
		if err := m.feature(f); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"file": args[i], "feature": f.Feature.ID, "added": m.added - added, "updated": m.updated - updated})
	}
	if len(m.clashes) > 0 {
		return nil, conflictErr("these records differ between the file and this database in a way import can't settle, so nothing was imported:\n- %s",
			strings.Join(m.clashes, "\n- "))
	}
	// A format 1 file comes from an aat without sign-off, so a story already
	// under way in it arrives unsigned. Treat it as the migration does.
	if err := backfillAgreed(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, validationErr("the file refers to records it doesn't hold, so nothing was imported: %v", err)
	}
	return map[string]any{"imported": results}, nil
}

// checkShared catches a file that isn't aat's, or is from a newer aat,
// before anything is read into the database.
func checkShared(f sharedFile) error {
	switch {
	case f.Format == 0:
		return fmt.Errorf("it has no format line")
	case f.Format > shareFormat:
		return fmt.Errorf("it was written by a newer aat (format %d; this aat reads up to %d); upgrade aat", f.Format, shareFormat)
	case !isID(f.Feature.ID, featureIDLen):
		return fmt.Errorf("feature id %q isn't an aat id", f.Feature.ID)
	}
	for _, st := range f.Stories {
		if !isID(st.ID, storyIDLen) {
			return fmt.Errorf("story id %q isn't an aat id", st.ID)
		}
		for _, sc := range st.Scenarios {
			if !isID(sc.ID, scenarioIDLen) {
				return fmt.Errorf("scenario id %q isn't an aat id", sc.ID)
			}
			for _, l := range sc.Lines {
				if !slices.Contains([]string{"Given", "When", "Then", "And", "But"}, l.Keyword) {
					return fmt.Errorf("scenario %s has a line starting %q", sc.ID, l.Keyword)
				}
			}
		}
		for _, d := range st.Deleted {
			if !isID(d.ID, scenarioIDLen) {
				return fmt.Errorf("deleted scenario id %q isn't an aat id", d.ID)
			}
		}
		for _, q := range st.Questions {
			if !isID(q.ID, questionIDLen) {
				return fmt.Errorf("question id %q isn't an aat id", q.ID)
			}
		}
	}
	return nil
}

type merger struct {
	ctx            context.Context
	tx             *sql.Tx
	clashes        []string
	added, updated int
}

func (m *merger) clash(format string, a ...any) {
	m.clashes = append(m.clashes, fmt.Sprintf(format, a...))
}

func (m *merger) exec(query string, args ...any) error {
	_, err := m.tx.ExecContext(m.ctx, query, args...)
	return err
}

// null stores an empty string as NULL, for the columns that hold NULL when unset.
func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// forward settles a group of fields that are filled in once and then never
// change. It reports whether to take the incoming values; a side that hasn't
// filled them in yet is behind, and two different fillings are a clash.
func (m *merger) forward(what string, local, incoming []string) bool {
	empty := func(vs []string) bool {
		return !slices.ContainsFunc(vs, func(v string) bool { return v != "" })
	}
	switch {
	case slices.Equal(local, incoming), empty(incoming):
		return false
	case empty(local):
		return true
	}
	m.clash("%s differently on each side", what)
	return false
}

func (m *merger) feature(f sharedFile) error {
	in := f.Feature
	var title, description, createdAt string
	err := m.tx.QueryRowContext(m.ctx, "SELECT title, description, created_at FROM features WHERE id = ?", in.ID).Scan(&title, &description, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := m.exec("INSERT INTO features (id, title, description, created_at) VALUES (?, ?, ?, ?)", in.ID, in.Title, in.Description, in.CreatedAt); err != nil {
			return err
		}
		m.added++
	case err != nil:
		return err
	case title != in.Title || description != in.Description || createdAt != in.CreatedAt:
		m.clash("feature %s (%q): its title or description differs", in.ID, in.Title)
	}
	for _, st := range f.Stories {
		if err := m.story(in.ID, st); err != nil {
			return err
		}
	}
	return nil
}

func (m *merger) story(featureID string, in sharedStory) error {
	var feature, title, description, createdAt, agreedAt, agreeNote, acceptedAt, acceptNote string
	err := m.tx.QueryRowContext(m.ctx, `SELECT feature_id, title, description, created_at, COALESCE(agreed_at, ''), agree_note, COALESCE(accepted_at, ''), accept_note
		FROM stories WHERE id = ?`, in.ID).Scan(&feature, &title, &description, &createdAt, &agreedAt, &agreeNote, &acceptedAt, &acceptNote)
	who := fmt.Sprintf("story %s (%q)", in.ID, in.Title)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := m.exec("INSERT INTO stories (id, feature_id, title, description, created_at, agreed_at, agree_note, accepted_at, accept_note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			in.ID, featureID, in.Title, in.Description, in.CreatedAt, null(in.AgreedAt), in.AgreeNote, null(in.AcceptedAt), in.AcceptNote); err != nil {
			return err
		}
		m.added++
	case err != nil:
		return err
	case feature != featureID:
		m.clash("%s: it belongs to a different feature here", who)
		return nil
	default:
		if title != in.Title || description != in.Description || createdAt != in.CreatedAt {
			m.clash("%s: its title or description was edited on one side", who)
		}
		if m.forward(who+": signed off", []string{agreedAt, agreeNote}, []string{in.AgreedAt, in.AgreeNote}) {
			if err := m.exec("UPDATE stories SET agreed_at = ?, agree_note = ? WHERE id = ?", in.AgreedAt, in.AgreeNote, in.ID); err != nil {
				return err
			}
			m.updated++
		}
		if m.forward(who+": accepted", []string{acceptedAt, acceptNote}, []string{in.AcceptedAt, in.AcceptNote}) {
			if err := m.exec("UPDATE stories SET accepted_at = ?, accept_note = ? WHERE id = ?", in.AcceptedAt, in.AcceptNote, in.ID); err != nil {
				return err
			}
			m.updated++
		}
	}
	for _, d := range in.Deleted {
		if err := m.deleted(in.ID, d); err != nil {
			return err
		}
	}
	for _, q := range in.Questions {
		if err := m.question(in.ID, q); err != nil {
			return err
		}
	}
	for _, sc := range in.Scenarios {
		if err := m.scenario(in.ID, sc); err != nil {
			return err
		}
	}
	return nil
}

// placeTaken reports whether another scenario or check-in already holds this
// place in the story's order: two people added to the same story.
func (m *merger) placeTaken(storyID string, position int, id string) (bool, error) {
	var other string
	err := m.tx.QueryRowContext(m.ctx, `
		SELECT id FROM scenarios WHERE story_id = ? AND position = ? AND id != ?
		UNION ALL
		SELECT id FROM questions WHERE story_id = ? AND position = ? AND id != ?
		LIMIT 1`, storyID, position, id, storyID, position, id).Scan(&other)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (m *merger) question(storyID string, in sharedQuestion) error {
	who := fmt.Sprintf("question %s", in.ID)
	var story, text, note, createdAt, resolvedAt string
	var position int
	err := m.tx.QueryRowContext(m.ctx, `SELECT story_id, text, COALESCE(position, 0), note, created_at, COALESCE(resolved_at, '')
		FROM questions WHERE id = ?`, in.ID).Scan(&story, &text, &position, &note, &createdAt, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if in.Position > 0 {
			if taken, err := m.placeTaken(storyID, in.Position, in.ID); err != nil || taken {
				if taken {
					m.clash("%s: its place in story %s is taken here by something else", who, storyID)
				}
				return err
			}
		}
		var pos any
		if in.Position > 0 {
			pos = in.Position
		}
		if err := m.exec("INSERT INTO questions (id, story_id, text, position, note, created_at, resolved_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			in.ID, storyID, in.Text, pos, in.Note, in.CreatedAt, null(in.ResolvedAt)); err != nil {
			return err
		}
		m.added++
		return m.addAnswers(in.ID, 0, in.Rounds, in.Corrections)
	}
	if err != nil {
		return err
	}
	switch {
	case story != storyID:
		m.clash("%s: it belongs to a different story here", who)
		return nil
	case text != in.Text || createdAt != in.CreatedAt:
		m.clash("%s: its text was edited on one side", who)
		return nil
	case position != in.Position:
		m.clash("%s: it was moved on one side", who)
		return nil
	}

	rounds, err := m.rounds(in.ID)
	if err != nil {
		return err
	}
	corrections, err := m.corrections(in.ID)
	if err != nil {
		return err
	}
	// Corrections only grow. The side with more of them has the corrected
	// text; if they grew apart, neither side's text can be trusted.
	ahead := "neither"
	switch {
	case slices.Equal(corrections, in.Corrections):
		ahead = "same"
	case len(in.Corrections) > len(corrections) && slices.Equal(corrections, in.Corrections[:len(corrections)]):
		ahead = "file"
	case len(corrections) > len(in.Corrections) && slices.Equal(in.Corrections, corrections[:len(in.Corrections)]):
		ahead = "here"
	default:
		m.clash("%s: its answers were corrected differently on each side", who)
		return nil
	}
	differs := func(what string) bool {
		if ahead == "same" {
			m.clash("%s: %s differs on each side", who, what)
		}
		return ahead == "file"
	}

	changed := false
	for i := range min(len(rounds), len(in.Rounds)) {
		if rounds[i].CreatedAt != in.Rounds[i].CreatedAt {
			m.clash("%s: round %d was answered differently on each side", who, i+1)
			return nil
		}
		if rounds[i].Answer != in.Rounds[i].Answer && differs(fmt.Sprintf("round %d", i+1)) {
			if err := m.exec("UPDATE question_rounds SET answer = ? WHERE question_id = ? AND position = ?", in.Rounds[i].Answer, in.ID, i+1); err != nil {
				return err
			}
			changed = true
		}
	}
	switch {
	case resolvedAt == "" && in.ResolvedAt != "":
		if err := m.exec("UPDATE questions SET resolved_at = ?, note = ? WHERE id = ?", in.ResolvedAt, in.Note, in.ID); err != nil {
			return err
		}
		changed = true
	case resolvedAt != "" && in.ResolvedAt != "" && resolvedAt != in.ResolvedAt:
		m.clash("%s: resolved differently on each side", who)
	case resolvedAt != "" && in.ResolvedAt != "" && note != in.Note && differs("its resolve note"):
		if err := m.exec("UPDATE questions SET note = ? WHERE id = ?", in.Note, in.ID); err != nil {
			return err
		}
		changed = true
	}
	var newRounds []sharedRound
	if len(in.Rounds) > len(rounds) {
		newRounds = in.Rounds[len(rounds):]
	}
	var newCorrections []sharedCorrection
	if ahead == "file" {
		newCorrections = in.Corrections[len(corrections):]
	}
	if changed || len(newRounds) > 0 || len(newCorrections) > 0 {
		m.updated++
	}
	return m.addAnswers(in.ID, len(rounds), newRounds, newCorrections)
}

// addAnswers adds rounds after the first `after`, and corrections, to a question.
func (m *merger) addAnswers(id string, after int, rounds []sharedRound, corrections []sharedCorrection) error {
	for i, r := range rounds {
		if err := m.exec("INSERT INTO question_rounds (question_id, position, answer, created_at) VALUES (?, ?, ?, ?)", id, after+i+1, r.Answer, r.CreatedAt); err != nil {
			return err
		}
	}
	for _, c := range corrections {
		if err := m.exec("INSERT INTO question_corrections (question_id, target, was, reason, created_at) VALUES (?, ?, ?, ?, ?)",
			id, c.Target, c.Was, c.Reason, c.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (m *merger) rounds(id string) ([]sharedRound, error) {
	rows, err := m.tx.QueryContext(m.ctx, "SELECT answer, created_at FROM question_rounds WHERE question_id = ? ORDER BY position", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sharedRound
	for rows.Next() {
		var r sharedRound
		if err := rows.Scan(&r.Answer, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *merger) corrections(id string) ([]sharedCorrection, error) {
	rows, err := m.tx.QueryContext(m.ctx, "SELECT target, was, reason, created_at FROM question_corrections WHERE question_id = ? ORDER BY created_at, rowid", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sharedCorrection
	for rows.Next() {
		var c sharedCorrection
		if err := rows.Scan(&c.Target, &c.Was, &c.Reason, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// deleted takes a draft scenario's deletion: deleted on one side means
// deleted on both, unless the other side has started building it.
func (m *merger) deleted(storyID string, in sharedDeleted) error {
	if gone, err := m.isDeleted(in.ID); err != nil || gone {
		return err
	}
	var title, handedOut, closedAt, replacedBy, retiredBy string
	var replaces int
	err := m.tx.QueryRowContext(m.ctx, `
		SELECT title, COALESCE(handed_out_at, ''), COALESCE(closed_at, ''), COALESCE(replaced_by, ''), COALESCE(retired_by, ''),
			(SELECT COUNT(*) FROM scenarios r WHERE r.replaced_by = scenarios.id)
		FROM scenarios WHERE id = ?`, in.ID).Scan(&title, &handedOut, &closedAt, &replacedBy, &retiredBy, &replaces)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case handedOut != "" || closedAt != "" || replacedBy != "" || retiredBy != "" || replaces > 0:
		m.clash("scenario %s (%q): deleted as a draft in the file, but it has been handed out or built here", in.ID, title)
		return nil
	default:
		if err := m.exec("DELETE FROM scenario_lines WHERE scenario_id = ?", in.ID); err != nil {
			return err
		}
		if err := m.exec("DELETE FROM scenarios WHERE id = ?", in.ID); err != nil {
			return err
		}
	}
	m.updated++
	return m.exec("INSERT INTO deleted_scenarios (id, story_id, deleted_at) VALUES (?, ?, ?)", in.ID, storyID, in.DeletedAt)
}

func (m *merger) isDeleted(id string) (bool, error) {
	var one int
	err := m.tx.QueryRowContext(m.ctx, "SELECT 1 FROM deleted_scenarios WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (m *merger) scenario(storyID string, in sharedScenario) error {
	who := fmt.Sprintf("scenario %s (%q)", in.ID, in.Title)
	if gone, err := m.isDeleted(in.ID); err != nil || gone {
		if gone && (in.HandedOutAt != "" || in.ClosedAt != "") {
			m.clash("%s: deleted as a draft here, but it has been handed out or built in the file", who)
		}
		return err
	}
	var here sharedScenario
	var story string
	err := m.tx.QueryRowContext(m.ctx, `
		SELECT story_id, position, title, description, created_at, COALESCE(question_id, ''), COALESCE(handed_out_at, ''),
			test_file, test_name, COALESCE(closed_at, ''), COALESCE(replaced_by, ''), COALESCE(retired_by, ''),
			change_reason, COALESCE(changed_at, ''), COALESCE(removed_at, '')
		FROM scenarios WHERE id = ?`, in.ID).Scan(&story, &here.Position, &here.Title, &here.Description, &here.CreatedAt, &here.QuestionID,
		&here.HandedOutAt, &here.TestFile, &here.Test, &here.ClosedAt, &here.ReplacedBy, &here.RetiredBy, &here.Reason, &here.ChangedAt, &here.RemovedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if taken, err := m.placeTaken(storyID, in.Position, in.ID); err != nil || taken {
			if taken {
				m.clash("%s: its place in story %s is taken here by something else", who, storyID)
			}
			return err
		}
		if err := m.exec(`INSERT INTO scenarios (id, story_id, position, title, description, created_at, question_id, handed_out_at,
				test_file, test_name, closed_at, replaced_by, retired_by, change_reason, changed_at, removed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.ID, storyID, in.Position, in.Title, in.Description, in.CreatedAt, null(in.QuestionID), null(in.HandedOutAt),
			in.TestFile, in.Test, null(in.ClosedAt), null(in.ReplacedBy), null(in.RetiredBy), in.Reason, null(in.ChangedAt), null(in.RemovedAt)); err != nil {
			return err
		}
		lines := make([]Line, len(in.Lines))
		for i, l := range in.Lines {
			lines[i] = Line(l)
		}
		if err := insertLines(m.ctx, m.tx, in.ID, lines); err != nil {
			return err
		}
		m.added++
		return nil
	}
	if err != nil {
		return err
	}
	if story != storyID {
		m.clash("%s: it belongs to a different story here", who)
		return nil
	}
	lines, err := m.lines(in.ID)
	if err != nil {
		return err
	}
	switch {
	case here.Title != in.Title || here.Description != in.Description || !slices.Equal(lines, in.Lines) ||
		here.CreatedAt != in.CreatedAt || here.QuestionID != in.QuestionID:
		m.clash("%s: its wording was edited on one side", who)
		return nil
	case here.Position != in.Position:
		m.clash("%s: it was moved on one side", who)
		return nil
	}

	type step struct {
		what            string
		local, incoming []string
		update          string
		args            []any
	}
	steps := []step{
		{"handed out", []string{here.HandedOutAt}, []string{in.HandedOutAt},
			"UPDATE scenarios SET handed_out_at = ? WHERE id = ?", []any{in.HandedOutAt}},
		{"closed", []string{here.TestFile, here.Test, here.ClosedAt}, []string{in.TestFile, in.Test, in.ClosedAt},
			"UPDATE scenarios SET test_file = ?, test_name = ?, closed_at = ? WHERE id = ?", []any{in.TestFile, in.Test, in.ClosedAt}},
		{"replaced or retired", []string{here.ReplacedBy, here.RetiredBy, here.Reason, here.ChangedAt}, []string{in.ReplacedBy, in.RetiredBy, in.Reason, in.ChangedAt},
			"UPDATE scenarios SET replaced_by = ?, retired_by = ?, change_reason = ?, changed_at = ? WHERE id = ?",
			[]any{null(in.ReplacedBy), null(in.RetiredBy), in.Reason, in.ChangedAt}},
		{"removed", []string{here.RemovedAt}, []string{in.RemovedAt},
			"UPDATE scenarios SET removed_at = ? WHERE id = ?", []any{in.RemovedAt}},
	}
	changed := false
	for _, st := range steps {
		if m.forward(who+": "+st.what, st.local, st.incoming) {
			if err := m.exec(st.update, append(st.args, in.ID)...); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		m.updated++
	}
	return nil
}

func (m *merger) lines(id string) ([]sharedLine, error) {
	rows, err := m.tx.QueryContext(m.ctx, "SELECT keyword, text FROM scenario_lines WHERE scenario_id = ? ORDER BY position", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sharedLine{}
	for rows.Next() {
		var l sharedLine
		if err := rows.Scan(&l.Keyword, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
