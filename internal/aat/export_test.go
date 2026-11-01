package aat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportingAStory(t *testing.T) {
	// reviewed sets up a story that has been through the whole loop: two
	// scenarios (the first closed), a check-in the user answered, and a
	// question that added a scenario already closed with its test.
	reviewed := func(t *testing.T) (string, string) {
		t.Helper()
		dir, story, ids := storyWithScenarios(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		var checkIn questionOut
		mustRun(t, dir, "", &checkIn, "question", "plan", "--story", story, "--body", "Show the user the export menu. Does it look right?")
		mustRun(t, dir, "", nil, "question", "answer", checkIn.ID, "--body", "Looks fine, but right-align it.")
		mustRun(t, dir, "", nil, "question", "resolve", checkIn.ID, "--note", "Right-aligned the menu.")
		q := ask(t, dir, story, "What about empty reports?")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "Disable the button.")
		os.WriteFile(filepath.Join(dir, "tests", "Feature", "EmptyReportTest.php"),
			[]byte("<?php\nit('disables export for an empty report', fn () => true);\n"), 0o644)
		mustRun(t, dir, emptyReportScenario, nil, "question", "resolve", q.ID, "--note", "Empty reports disable the button.",
			"--scenarios", "-", "--test-file", "tests/Feature/EmptyReportTest.php", "--test", "disables export for an empty report")
		return dir, story
	}

	t.Run("it reads as a review of the story, with each scenario's test", func(t *testing.T) {
		dir, story := reviewed(t)
		out, errOut, code := run(t, dir, "", "export", story)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		for _, want := range []string{
			"---\nid: " + story + "\nkind: story\n",
			"## Add xlsx\n",
			"Feature: Exporting reports",
			"why Add xlsx",
			"```gherkin\nScenario: Exporting as xlsx\n  Given a report with some rows\n",
			"Closed by the test `exports a report as xlsx` in `tests/Feature/ExportTest.php`. aat found the test name in the file; it did not run the test.",
			"Open: not built yet.",
			"> Show the user the export menu. Does it look right?",
			"1. Looks fine, but right-align it.",
			"Settled: Right-aligned the menu.",
			"## Questions raised while building",
			"> What about empty reports?",
			"Settled: Empty reports disable the button.",
			"Scenario: Exporting an empty report",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("export is missing %q", want)
			}
		}
		if t.Failed() {
			t.Logf("export =\n%s", out)
		}
	})

	t.Run("it follows the order the builder met things in", func(t *testing.T) {
		dir, story := reviewed(t)
		out, _, _ := run(t, dir, "", "export", story)
		order := []string{"Scenario: Exporting as xlsx", "Scenario: Choosing the format", "Show the user the export menu", "Scenario: Exporting an empty report"}
		last := -1
		for _, s := range order {
			i := strings.Index(out, s)
			if i <= last {
				t.Fatalf("%q is out of order in:\n%s", s, out)
			}
			last = i
		}
	})

	t.Run("--output writes the file", func(t *testing.T) {
		dir, story := reviewed(t)
		var written struct {
			Path string `json:"path"`
		}
		mustRun(t, dir, "", &written, "export", story, "--output", "review.md")
		got, err := os.ReadFile(filepath.Join(dir, "review.md"))
		if err != nil || written.Path != "review.md" || !strings.Contains(string(got), "## Add xlsx") {
			t.Fatalf("path = %q, err = %v, file =\n%s", written.Path, err, got)
		}
	})

	t.Run("an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "export", "nope00"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

func TestExportEdges(t *testing.T) {
	t.Run("--output never overwrites a file that isn't an export of the story", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		notes := "# My own notes\n"
		os.WriteFile(filepath.Join(dir, "review.md"), []byte(notes), 0o644)
		if code := errorCode(t, dir, "", "export", story, "--output", "review.md"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "review.md")); string(got) != notes {
			t.Fatalf("the file was changed:\n%s", got)
		}
		os.Remove(filepath.Join(dir, "review.md"))
		mustRun(t, dir, "", nil, "export", story, "--output", "review.md")
		mustRun(t, dir, "", nil, "export", story, "--output", "review.md")
	})

	t.Run("backticks in a description don't break the gherkin block", func(t *testing.T) {
		var b strings.Builder
		renderScenario(&b, "### 1. x", Scenario{Title: "x", Description: "Run ```make``` first.", Status: "open"})
		if !strings.Contains(b.String(), "````gherkin\n") || !strings.Contains(b.String(), "\n````\n") {
			t.Fatalf("render =\n%s", b.String())
		}
	})
}
