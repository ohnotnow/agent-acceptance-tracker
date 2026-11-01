package aat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type closeOut struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	TestFile string   `json:"test_file"`
	Test     string   `json:"test"`
	Check    string   `json:"check"`
	Warnings []string `json:"warnings"`
}

// storyWithScenarios sets up a project with one story holding twoScenarios,
// plus a test file containing two test names. It returns the project dir,
// the story id and the scenario ids in order.
func storyWithScenarios(t *testing.T) (string, string, []string) {
	t.Helper()
	dir := newProject(t)
	s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, twoScenarios, &added, "scenario", "add", "--story", s.ID, "--body", "-")
	os.MkdirAll(filepath.Join(dir, "tests", "Feature"), 0o755)
	os.WriteFile(filepath.Join(dir, "tests", "Feature", "ExportTest.php"), []byte(`<?php
it('exports a report as xlsx', function () {});
it('offers csv and xlsx in the export menu', function () {});
`), 0o644)
	return dir, s.ID, []string{added.Scenarios[0].ID, added.Scenarios[1].ID}
}

func TestClosingAScenarioWithItsTest(t *testing.T) {
	t.Run("it is closed with its test, and next moves on", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if closed.Status != "closed" || closed.TestFile != "tests/Feature/ExportTest.php" || closed.Test != "exports a report as xlsx" {
			t.Fatalf("closed = %#v", closed)
		}
		if closed.Check != "found the test name in the file" || len(closed.Warnings) != 0 {
			t.Fatalf("check = %q, warnings = %v", closed.Check, closed.Warnings)
		}

		var next struct {
			Scenario *scenarioOut `json:"scenario"`
		}
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Scenario == nil || next.Scenario.ID != ids[1] {
			t.Fatalf("next = %#v, want the second scenario", next.Scenario)
		}
	})

	t.Run("the story shows each scenario's status and test", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		var shown struct {
			Scenarios []struct {
				Status   string `json:"status"`
				TestFile string `json:"test_file"`
				Test     string `json:"test"`
			} `json:"scenarios"`
		}
		mustRun(t, dir, "", &shown, "story", "show", story)
		first, second := shown.Scenarios[0], shown.Scenarios[1]
		if first.Status != "closed" || first.TestFile != "tests/Feature/ExportTest.php" || first.Test != "exports a report as xlsx" {
			t.Fatalf("first = %#v", first)
		}
		if second.Status != "open" || second.TestFile != "" {
			t.Fatalf("second = %#v", second)
		}
	})

	t.Run("when every scenario is closed there is nothing next", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		out, _, code := run(t, dir, "", "next", "--story", story)
		if code != 0 || strings.TrimSpace(out) != `{"scenario":null}` {
			t.Fatalf("exit %d, out %q", code, out)
		}
	})

	t.Run("a test file path from a subdirectory is stored relative to the project", func(t *testing.T) {
		dir, _, ids := storyWithScenarios(t)
		var closed closeOut
		mustRun(t, filepath.Join(dir, "tests"), "", &closed, "close", ids[0], "--test-file", "Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if closed.TestFile != "tests/Feature/ExportTest.php" {
			t.Fatalf("test_file = %q", closed.TestFile)
		}
	})

	t.Run("a free-form scenario is closed with a test like any other", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, "Scenario: Feels quick\n  Exporting should feel instant on an ordinary report.\n", &added,
			"scenario", "add", "--story", story, "--body", "-")
		id := added.Scenarios[0].ID

		if code := errorCode(t, dir, "", "close", id); code != "usage" {
			t.Fatalf("closing without a test: code = %q, want usage", code)
		}
		os.WriteFile(filepath.Join(dir, "tests", "Feature", "SpeedTest.php"), []byte("it('exports an ordinary report in under a second', fn);\n"), 0o644)
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", id, "--test-file", "tests/Feature/SpeedTest.php", "--test", "exports an ordinary report in under a second")
		if closed.Status != "closed" {
			t.Fatalf("closed = %#v", closed)
		}
	})

	t.Run("reusing another scenario's test closes it with a warning", func(t *testing.T) {
		dir, _, ids := storyWithScenarios(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if closed.Status != "closed" || len(closed.Warnings) != 1 || !strings.Contains(closed.Warnings[0], ids[0]) {
			t.Fatalf("closed = %#v, want one warning naming %s", closed, ids[0])
		}
	})
}

func TestClosingAScenarioIsRefused(t *testing.T) {
	cases := []struct {
		name, file, test, want string
	}{
		{"when the test file doesn't exist", "tests/Feature/Missing.php", "exports a report as xlsx", "validation"},
		{"when the test name isn't in the file", "tests/Feature/ExportTest.php", "exports a report as pdf", "validation"},
		{"when the test file is outside the project", "../elsewhere.php", "exports a report as xlsx", "validation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, story, ids := storyWithScenarios(t)
			if code := errorCode(t, dir, "", "close", ids[0], "--test-file", c.file, "--test", c.test); code != c.want {
				t.Fatalf("code = %q, want %q", code, c.want)
			}
			var next struct {
				Scenario *scenarioOut `json:"scenario"`
			}
			mustRun(t, dir, "", &next, "next", "--story", story)
			if next.Scenario == nil || next.Scenario.ID != ids[0] {
				t.Fatalf("scenario should still be open, next = %#v", next.Scenario)
			}
		})
	}

	t.Run("when it is already closed", func(t *testing.T) {
		dir, _, ids := storyWithScenarios(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if code := errorCode(t, dir, "", "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("when the scenario doesn't exist", func(t *testing.T) {
		dir, _, _ := storyWithScenarios(t)
		if code := errorCode(t, dir, "", "close", "nope0000", "--test-file", "tests/Feature/ExportTest.php", "--test", "x"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("without a test file and test name", func(t *testing.T) {
		dir, _, ids := storyWithScenarios(t)
		if code := errorCode(t, dir, "", "close", ids[0], "--test-file", "tests/Feature/ExportTest.php"); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}
