package aat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI in-process from dir and returns stdout, stderr and the exit code.
func run(t *testing.T, dir, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, dir, Streams{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
	})
	return out.String(), errOut.String(), code
}

// mustRun runs a command that is expected to succeed and decodes its JSON output into v.
func mustRun(t *testing.T, dir, stdin string, v any, args ...string) {
	t.Helper()
	out, errOut, code := run(t, dir, stdin, args...)
	if code != 0 {
		t.Fatalf("aat %v: exit %d, stderr: %s", args, code, errOut)
	}
	if v != nil {
		if err := json.Unmarshal([]byte(out), v); err != nil {
			t.Fatalf("aat %v: decode %q: %v", args, out, err)
		}
	}
}

// errorCode runs a command that is expected to fail and returns its JSON error code.
func errorCode(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	_, errOut, code := run(t, dir, stdin, args...)
	if code == 0 {
		t.Fatalf("aat %v: expected failure, got exit 0", args)
	}
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(errOut), &e); err != nil {
		t.Fatalf("aat %v: decode error %q: %v", args, errOut, err)
	}
	return e.Error.Code
}

// newProject makes a temporary git-looking directory and runs aat init in it.
func newProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "", nil, "init")
	return dir
}

type featureOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type storyOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Feature     struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"feature"`
	Scenarios []scenarioOut `json:"scenarios"`
}

type scenarioOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Lines       []struct {
		Keyword string `json:"keyword"`
		Text    string `json:"text"`
	} `json:"lines"`
}

func addFeature(t *testing.T, dir, title string) featureOut {
	t.Helper()
	var f featureOut
	mustRun(t, dir, "", &f, "feature", "add", "--title", title, "--description", "about "+title)
	return f
}

func addStory(t *testing.T, dir, featureID, title string) storyOut {
	t.Helper()
	var s storyOut
	mustRun(t, dir, "", &s, "story", "add", "--feature", featureID, "--title", title, "--description", "why "+title)
	return s
}

const twoScenarios = `Scenario: Exporting as xlsx
  Given a report with some rows
  When I export it as xlsx
  Then I get an xlsx file
  And it has the same rows as the csv export

Scenario: Choosing the format
  Given a report
  When I open the export menu
  Then I can choose csv or xlsx
`

func TestSettingUpAatInAProject(t *testing.T) {
	t.Run("init creates the database and ignores it in git", func(t *testing.T) {
		dir := newProject(t)
		if _, err := os.Stat(filepath.Join(dir, ".aat", "aat.db")); err != nil {
			t.Fatalf("database not created: %v", err)
		}
		gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(gi), ".aat/") {
			t.Fatalf(".gitignore = %q, want it to contain .aat/", gi)
		}
	})

	t.Run("a second init keeps the gitignore entry single", func(t *testing.T) {
		dir := newProject(t)
		mustRun(t, dir, "", nil, "init")
		gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if n := strings.Count(string(gi), ".aat/"); n != 1 {
			t.Fatalf(".aat/ appears %d times in .gitignore, want 1", n)
		}
	})

	t.Run("init keeps existing gitignore lines", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, ".git"), 0o755)
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".ait/\n.ant/"), 0o644)
		mustRun(t, dir, "", nil, "init")
		gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if string(gi) != ".ait/\n.ant/\n.aat/\n" {
			t.Fatalf(".gitignore = %q", gi)
		}
	})

	t.Run("a fresh project has an empty list of features", func(t *testing.T) {
		dir := newProject(t)
		var list struct {
			Features []featureOut `json:"features"`
		}
		mustRun(t, dir, "", &list, "feature", "list")
		if list.Features == nil || len(list.Features) != 0 {
			t.Fatalf("features = %#v, want an empty list", list.Features)
		}
	})

	t.Run("commands before init are refused", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, ".git"), 0o755)
		if code := errorCode(t, dir, "", "feature", "list"); code != "uninitialised" {
			t.Fatalf("code = %q, want uninitialised", code)
		}
	})

	t.Run("init from a subdirectory uses the git root", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, ".git"), 0o755)
		sub := filepath.Join(dir, "app", "Models")
		os.MkdirAll(sub, 0o755)
		mustRun(t, sub, "", nil, "init")
		if _, err := os.Stat(filepath.Join(dir, ".aat", "aat.db")); err != nil {
			t.Fatalf("database not at git root: %v", err)
		}
	})
}

func TestAddingAFeature(t *testing.T) {
	t.Run("it gets an id and appears in the list of features", func(t *testing.T) {
		dir := newProject(t)
		f := addFeature(t, dir, "Exporting reports")
		if !isID(f.ID, featureIDLen) {
			t.Fatalf("id = %q, want %d base62 characters", f.ID, featureIDLen)
		}
		var list struct {
			Features []featureOut `json:"features"`
		}
		mustRun(t, dir, "", &list, "feature", "list")
		if len(list.Features) != 1 || list.Features[0].ID != f.ID || list.Features[0].Title != "Exporting reports" {
			t.Fatalf("features = %#v", list.Features)
		}
	})

	t.Run("a feature needs a title", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "feature", "add", "--description", "x"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("the description can come from stdin", func(t *testing.T) {
		dir := newProject(t)
		var f featureOut
		mustRun(t, dir, "Everything about exports.\n", &f, "feature", "add", "--title", "Exporting reports", "--description", "-")
		if f.Description != "Everything about exports." {
			t.Fatalf("description = %q", f.Description)
		}
	})
}

func TestAddingAStory(t *testing.T) {
	t.Run("it belongs to a feature and shows with it", func(t *testing.T) {
		dir := newProject(t)
		f := addFeature(t, dir, "Exporting reports")
		s := addStory(t, dir, f.ID, "Add xlsx alongside csv")
		if !isID(s.ID, storyIDLen) {
			t.Fatalf("id = %q, want %d base62 characters", s.ID, storyIDLen)
		}
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", s.ID)
		if shown.Title != "Add xlsx alongside csv" || shown.Feature.ID != f.ID || shown.Feature.Title != "Exporting reports" {
			t.Fatalf("story = %#v", shown)
		}
		if shown.Scenarios == nil || len(shown.Scenarios) != 0 {
			t.Fatalf("scenarios = %#v, want an empty list", shown.Scenarios)
		}
	})

	t.Run("an unknown feature is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "story", "add", "--feature", "nope00", "--title", "x"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("showing an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "story", "show", "nope00"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

func TestAddingScenariosToAStory(t *testing.T) {
	t.Run("they show under the story in the order added, line by line", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")

		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, twoScenarios, &added, "scenario", "add", "--story", s.ID, "--body", "-")
		if len(added.Scenarios) != 2 || !isID(added.Scenarios[0].ID, scenarioIDLen) {
			t.Fatalf("added = %#v", added.Scenarios)
		}

		mustRun(t, dir, "Scenario: Large reports\n  Given a report with a million rows\n  Then the export still works\n", nil,
			"scenario", "add", "--story", s.ID, "--body", "-")

		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", s.ID)
		var titles []string
		for _, sc := range shown.Scenarios {
			titles = append(titles, sc.Title)
		}
		if got := strings.Join(titles, " | "); got != "Exporting as xlsx | Choosing the format | Large reports" {
			t.Fatalf("titles = %s", got)
		}
		first := shown.Scenarios[0]
		if len(first.Lines) != 4 || first.Lines[0].Keyword != "Given" || first.Lines[0].Text != "a report with some rows" ||
			first.Lines[3].Keyword != "And" || first.Lines[3].Text != "it has the same rows as the csv export" {
			t.Fatalf("lines = %#v", first.Lines)
		}
	})

	t.Run("the body can come from a file", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		path := filepath.Join(t.TempDir(), "scenarios.feature")
		os.WriteFile(path, []byte(twoScenarios), 0o644)
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, "", &added, "scenario", "add", "--story", s.ID, "--body", "@"+path)
		if len(added.Scenarios) != 2 {
			t.Fatalf("added = %#v", added.Scenarios)
		}
	})

	t.Run("a batch with unsupported Gherkin stores nothing", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		body := twoScenarios + "\nScenario Outline: Many formats\n  Given a <format>\n\n  Examples:\n    | format |\n    | csv    |\n"
		if code := errorCode(t, dir, body, "scenario", "add", "--story", s.ID, "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", s.ID)
		if len(shown.Scenarios) != 0 {
			t.Fatalf("scenarios = %#v, want none stored", shown.Scenarios)
		}
	})

	t.Run("an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, twoScenarios, "scenario", "add", "--story", "nope00", "--body", "-"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("a body is required", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		if code := errorCode(t, dir, "", "scenario", "add", "--story", s.ID); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}

func TestAskingWhatIsNext(t *testing.T) {
	t.Run("it hands back the story's first scenario", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		mustRun(t, dir, twoScenarios, nil, "scenario", "add", "--story", s.ID, "--body", "-")
		var next struct {
			Scenario *scenarioOut `json:"scenario"`
		}
		mustRun(t, dir, "", &next, "next", "--story", s.ID)
		if next.Scenario == nil || next.Scenario.Title != "Exporting as xlsx" || len(next.Scenario.Lines) != 4 {
			t.Fatalf("next = %#v", next.Scenario)
		}
	})

	t.Run("a story with no scenarios has nothing next", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		out, _, code := run(t, dir, "", "next", "--story", s.ID)
		if code != 0 || strings.TrimSpace(out) != `{"scenario":null}` {
			t.Fatalf("exit %d, out %q", code, out)
		}
	})

	t.Run("an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "next", "--story", "nope00"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}
