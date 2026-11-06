package aat

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A story is a draft until the user signs it off with `story agree`; only
// then will next hand out its work (ait aat-L3pHO). `aat stories` lists every
// story with the one state it is in.

type agreedOut struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	AgreedAt  string `json:"agreed_at"`
	AgreeNote string `json:"agree_note"`
}

// agree signs a story off, as the writing session does once the user says yes.
func agree(t *testing.T, dir, story string) {
	t.Helper()
	mustRun(t, dir, "", nil, "story", "agree", story, "--note", "Yes, build it.")
}

// draftStory is storyWithScenarios before anyone has signed it off.
func draftStory(t *testing.T) (string, string, []string) {
	t.Helper()
	dir := newProject(t)
	s := addDraftStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, twoScenarios, &added, "scenario", "add", "--story", s.ID, "--body", "-")
	return dir, s.ID, []string{added.Scenarios[0].ID, added.Scenarios[1].ID}
}

func TestSigningOffAStory(t *testing.T) {
	t.Run("next refuses a draft story and hands nothing out, so it can still be edited", func(t *testing.T) {
		dir, story, ids := draftStory(t)
		code := errorCode(t, dir, "", "next", "--story", story)
		if code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		_, stderr, _ := run(t, dir, "", "next", "--story", story)
		if !strings.Contains(stderr, "draft") || !strings.Contains(stderr, "aat story agree") {
			t.Fatalf("stderr = %s", stderr)
		}
		var sc scenarioOut
		mustRun(t, dir, "", &sc, "scenario", "show", ids[0])
		if sc.HandedOutAt != "" {
			t.Fatalf("handed out: %#v", sc)
		}
		mustRun(t, dir, "Scenario: Exporting as xlsx\n  Given a report\n  Then I get an xlsx file\n", nil,
			"scenario", "edit", ids[0], "--body", "-")
	})

	t.Run("once agreed, next hands out work as before", func(t *testing.T) {
		dir, story, ids := draftStory(t)
		var agreed agreedOut
		mustRun(t, dir, "", &agreed, "story", "agree", story, "--note", "Looks right, go ahead.")
		if agreed.ID != story || agreed.AgreedAt == "" || agreed.AgreeNote != "Looks right, go ahead." {
			t.Fatalf("agree = %#v", agreed)
		}
		var next struct {
			Scenario *scenarioOut `json:"scenario"`
		}
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Scenario == nil || next.Scenario.ID != ids[0] {
			t.Fatalf("next = %#v", next.Scenario)
		}
		var shown agreedOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if shown.AgreedAt != agreed.AgreedAt || shown.AgreeNote != agreed.AgreeNote || shown.State != "building" {
			t.Fatalf("story show = %#v", shown)
		}
	})

	t.Run("agreeing twice, without a note, or an unknown story is refused", func(t *testing.T) {
		dir, story, _ := draftStory(t)
		if code := errorCode(t, dir, "", "story", "agree", story); code != "usage" {
			t.Fatalf("without a note: code = %q, want usage", code)
		}
		if code := errorCode(t, dir, "", "story", "agree", "nope00", "--note", "x"); code != "not_found" {
			t.Fatalf("unknown: code = %q, want not_found", code)
		}
		agree(t, dir, story)
		if code := errorCode(t, dir, "", "story", "agree", story, "--note", "Again."); code != "conflict" {
			t.Fatalf("twice: code = %q, want conflict", code)
		}
	})

	t.Run("a draft story can't be accepted", func(t *testing.T) {
		dir := newProject(t)
		s := addDraftStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Nothing in it yet")
		if code := errorCode(t, dir, "", "story", "accept", s.ID, "--note", "x"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("the export says whether and when the story was signed off", func(t *testing.T) {
		dir, story, _ := draftStory(t)
		out, _, _ := run(t, dir, "", "export", story)
		if !strings.Contains(out, "Not signed off yet: a draft.") {
			t.Fatalf("export =\n%s", out)
		}
		mustRun(t, dir, "", nil, "story", "agree", story, "--note", "Looks right, go ahead.")
		out, _, _ = run(t, dir, "", "export", story)
		if !strings.Contains(out, "Signed off on ") || !strings.Contains(out, "Looks right, go ahead.") {
			t.Fatalf("export =\n%s", out)
		}
	})

	t.Run("the sign-off travels in a YAML file", func(t *testing.T) {
		dir, story, _ := draftStory(t)
		agree(t, dir, story)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, dir))
		var shown agreedOut
		mustRun(t, theirs, "", &shown, "story", "show", story)
		if shown.AgreedAt == "" || shown.AgreeNote != "Yes, build it." {
			t.Fatalf("imported story = %#v", shown)
		}
	})

	t.Run("a file from an aat without sign-off brings a story under way in as agreed, and a draft as a draft", func(t *testing.T) {
		dir, story, _ := draftStory(t)
		f := addFeature(t, dir, "Other").ID
		draft := addDraftStory(t, dir, f, "Still a draft")
		mustRun(t, dir, twoScenarios, nil, "scenario", "add", "--story", draft.ID, "--body", "-")
		agree(t, dir, story)
		mustRun(t, dir, "", nil, "next", "--story", story)
		var old []string
		mustRun(t, dir, "", nil, "publish", "--yaml")
		for _, name := range []string{"exporting_reports.yaml", "other.yaml"} {
			path := filepath.Join(dir, "features", "yaml", name)
			var kept []string
			for _, l := range strings.Split(readFile(t, path), "\n") {
				if strings.Contains(l, "agreed_at:") || strings.Contains(l, "agree_note:") {
					continue
				}
				kept = append(kept, strings.Replace(l, "format: 2", "format: 1", 1))
			}
			os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644)
			old = append(old, path)
		}
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, append([]string{"import"}, old...)...)
		var shown agreedOut
		mustRun(t, theirs, "", &shown, "story", "show", story)
		if shown.State != "building" || shown.AgreeNote != migrationNote {
			t.Fatalf("under way = %#v", shown)
		}
		shown = agreedOut{}
		mustRun(t, theirs, "", &shown, "story", "show", draft.ID)
		if shown.State != "draft" {
			t.Fatalf("draft = %#v", shown)
		}
	})
}

type storiesOut struct {
	Stories []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		State   string `json:"state"`
		Feature struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"feature"`
		Progress struct {
			Closed int `json:"closed"`
			Total  int `json:"total"`
		} `json:"progress"`
		HeldBy      string `json:"held_by"`
		LastTouched string `json:"last_touched"`
	} `json:"stories"`
}

func (s storiesOut) states() map[string]string {
	m := map[string]string{}
	for _, st := range s.Stories {
		m[st.Title] = st.State
	}
	return m
}

// fourStories makes one story in each state, touched in the order draft,
// building, built, accepted, then touches the draft again.
func fourStories(t *testing.T) string {
	t.Helper()
	dir, built := finishedStory(t)
	var shown storyOut
	mustRun(t, dir, "", &shown, "story", "show", built)
	f := shown.Feature.ID

	accepted := addStory(t, dir, f, "Accepted one")
	mustRun(t, dir, "Scenario: Done\n  Then it is done\n", nil, "scenario", "add", "--story", accepted.ID, "--body", "-")
	var next struct {
		Scenario *scenarioOut `json:"scenario"`
	}
	mustRun(t, dir, "", &next, "next", "--story", accepted.ID)
	mustRun(t, dir, "", nil, "close", next.Scenario.ID, "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
	mustRun(t, dir, "", nil, "story", "accept", accepted.ID, "--note", "Happy.")

	building := addStory(t, dir, f, "Building one")
	mustRun(t, dir, twoScenarios, nil, "scenario", "add", "--story", building.ID, "--body", "-")
	mustRun(t, dir, "", &next, "next", "--story", building.ID)
	mustRun(t, dir, "", nil, "close", next.Scenario.ID, "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
	ask(t, dir, building.ID, "Should the button be green?")

	draft := addDraftStory(t, dir, f, "Draft one")
	mustRun(t, dir, twoScenarios, nil, "scenario", "add", "--story", draft.ID, "--body", "-")
	return dir
}

func TestListingStories(t *testing.T) {
	t.Run("with no flags it lists every story, each in one state", func(t *testing.T) {
		dir := fourStories(t)
		var out storiesOut
		mustRun(t, dir, "", &out, "stories")
		want := map[string]string{"Add xlsx": "built", "Accepted one": "accepted", "Building one": "building", "Draft one": "draft"}
		got := out.states()
		if len(got) != 4 {
			t.Fatalf("stories = %#v", out.Stories)
		}
		for title, state := range want {
			if got[title] != state {
				t.Fatalf("%s: state = %q, want %q (all: %#v)", title, got[title], state, got)
			}
		}
	})

	t.Run("each story shows its feature, progress, what holds it, and newest-touched comes first", func(t *testing.T) {
		dir := fourStories(t)
		var out storiesOut
		mustRun(t, dir, "", &out, "stories")
		if out.Stories[0].Title != "Draft one" {
			t.Fatalf("first = %q, want the draft, touched last", out.Stories[0].Title)
		}
		for i := 1; i < len(out.Stories); i++ {
			if out.Stories[i-1].LastTouched < out.Stories[i].LastTouched {
				t.Fatalf("not newest first: %#v", out.Stories)
			}
		}
		for _, st := range out.Stories {
			if st.Feature.Title != "Exporting reports" || st.LastTouched == "" {
				t.Fatalf("story = %#v", st)
			}
			if st.Title == "Building one" {
				if st.Progress.Closed != 1 || st.Progress.Total != 2 || st.HeldBy == "" {
					t.Fatalf("building one = %#v", st)
				}
			}
		}
	})

	t.Run("--state and --open filter it", func(t *testing.T) {
		dir := fourStories(t)
		var out storiesOut
		mustRun(t, dir, "", &out, "stories", "--state", "draft")
		if got := out.states(); len(got) != 1 || got["Draft one"] != "draft" {
			t.Fatalf("--state draft = %#v", got)
		}
		out = storiesOut{}
		mustRun(t, dir, "", &out, "stories", "--open")
		if got := out.states(); len(got) != 3 || got["Accepted one"] != "" {
			t.Fatalf("--open = %#v", got)
		}
		if code := errorCode(t, dir, "", "stories", "--state", "finished"); code != "usage" {
			t.Fatalf("unknown state: code = %q, want usage", code)
		}
		if code := errorCode(t, dir, "", "stories", "--state", "draft", "--open"); code != "usage" {
			t.Fatalf("both: code = %q, want usage", code)
		}
	})

	t.Run("listing never changes the database", func(t *testing.T) {
		dir := fourStories(t)
		before := dumpDB(t, dir)
		mustRun(t, dir, "", nil, "stories")
		mustRun(t, dir, "", nil, "feature", "list")
		mustRun(t, dir, "", nil, "story", "show", fourStoriesDraftID(t, dir))
		if after := dumpDB(t, dir); after != before {
			t.Fatalf("database changed:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("feature list counts each feature's stories by state", func(t *testing.T) {
		dir := fourStories(t)
		var out struct {
			Features []struct {
				Stories map[string]int `json:"stories"`
			} `json:"features"`
		}
		mustRun(t, dir, "", &out, "feature", "list")
		got := out.Features[0].Stories
		if len(got) != 4 || got["draft"] != 1 || got["building"] != 1 || got["built"] != 1 || got["accepted"] != 1 {
			t.Fatalf("counts = %#v", got)
		}
		empty := addFeature(t, dir, "Nothing yet")
		_ = empty
		out.Features = nil
		mustRun(t, dir, "", &out, "feature", "list")
		if len(out.Features[1].Stories) != 0 {
			t.Fatalf("empty feature counts = %#v", out.Features[1].Stories)
		}
	})
}

func fourStoriesDraftID(t *testing.T, dir string) string {
	t.Helper()
	var out storiesOut
	mustRun(t, dir, "", &out, "stories", "--state", "draft")
	return out.Stories[0].ID
}

// dumpDB renders every row the tests could change, to compare before and after.
func dumpDB(t *testing.T, dir string) string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, ".aat", "aat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var b strings.Builder
	for _, q := range []string{
		"SELECT id, COALESCE(agreed_at, ''), COALESCE(accepted_at, '') FROM stories ORDER BY id",
		"SELECT id, COALESCE(handed_out_at, ''), COALESCE(closed_at, '') FROM scenarios ORDER BY id",
		"SELECT id, COALESCE(resolved_at, '') FROM questions ORDER BY id",
	} {
		rows, err := db.QueryContext(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]string, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			b.WriteString(strings.Join(vals, "|") + "\n")
		}
		rows.Close()
	}
	return b.String()
}

func TestSignOffMigration(t *testing.T) {
	// A database from before the sign-off gate: one accepted story, one with a
	// handed-out scenario, one never touched, built by applying the migrations
	// before the sign-off one by hand.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_version (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL DEFAULT 0);
		INSERT INTO schema_version (id, version) VALUES (1, 0);`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version > 10 {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.apply(ctx, tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE schema_version SET version = ? WHERE id = 1", m.version); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		"INSERT INTO features (id, title, created_at) VALUES ('ffffff', 'F', '2026-01-01T00:00:00Z')",
		"INSERT INTO stories (id, feature_id, title, created_at, accepted_at, accept_note) VALUES ('acc000', 'ffffff', 'Accepted', '2026-01-01T00:00:00Z', '2026-01-05T00:00:00Z', 'ok')",
		"INSERT INTO scenarios (id, story_id, position, title, created_at, closed_at) VALUES ('accsc000', 'acc000', 1, 'S', '2026-01-01T00:00:00Z', '2026-01-03T00:00:00Z')",
		"INSERT INTO stories (id, feature_id, title, created_at) VALUES ('bld000', 'ffffff', 'Building', '2026-01-01T00:00:00Z')",
		"INSERT INTO scenarios (id, story_id, position, title, created_at, handed_out_at) VALUES ('bldsc000', 'bld000', 1, 'S', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')",
		"INSERT INTO stories (id, feature_id, title, created_at) VALUES ('drf000', 'ffffff', 'Draft', '2026-01-01T00:00:00Z')",
		"INSERT INTO scenarios (id, story_id, position, title, created_at) VALUES ('drfsc000', 'drf000', 1, 'S', '2026-01-01T00:00:00Z')",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	store, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for id, want := range map[string]string{"acc000": "2026-01-03T00:00:00Z", "bld000": "2026-01-02T00:00:00Z", "drf000": ""} {
		st, err := store.GetStory(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if st.AgreedAt != want {
			t.Fatalf("%s: agreed_at = %q, want %q", id, st.AgreedAt, want)
		}
		if want != "" && !strings.Contains(st.AgreeNote, "migration") {
			t.Fatalf("%s: agree_note = %q", id, st.AgreeNote)
		}
	}
}
