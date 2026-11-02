package aat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type questionOut struct {
	ID      string `json:"id"`
	StoryID string `json:"story_id"`
	Text    string `json:"text"`
	Status  string `json:"status"`
	Note    string `json:"note"`
	Rounds  []struct {
		Answer string `json:"answer"`
	} `json:"rounds"`
	Position   int           `json:"position"`
	ResolvedAt string        `json:"resolved_at"`
	Scenarios  []scenarioOut `json:"scenarios"`
	Check      string        `json:"check"`
	Warnings   []string      `json:"warnings"`
}

// nextID returns the id of whatever next hands out, scenario or question, or "" for nothing.
func nextID(t *testing.T, dir, story string) string {
	t.Helper()
	var next nextOut
	mustRun(t, dir, "", &next, "next", "--story", story)
	switch {
	case next.Question != nil:
		return next.Question.ID
	case next.Scenario != nil:
		return next.Scenario.ID
	case next.Retirement != nil:
		return next.Retirement.ID
	}
	return ""
}

// stepIDs lists the ids of a story's steps in order, separated by spaces.
func stepIDs(steps []stepOut) string {
	var ids []string
	for _, st := range steps {
		if st.Scenario != nil {
			ids = append(ids, st.Scenario.ID)
		} else if st.Question != nil {
			ids = append(ids, st.Question.ID)
		}
	}
	return strings.Join(ids, " ")
}

type nextOut struct {
	Scenario   *scenarioOut `json:"scenario"`
	Question   *questionOut `json:"question"`
	Retirement *scenarioOut `json:"retirement"`
}

func ask(t *testing.T, dir, story, text string) questionOut {
	t.Helper()
	var q questionOut
	mustRun(t, dir, "", &q, "question", "ask", "--story", story, "--body", text)
	return q
}

const emptyReportScenario = `Scenario: Exporting an empty report
  Given a report with no rows
  Then the export button is disabled
`

const twoNewScenarios = emptyReportScenario + `
Scenario: Exporting a one-row report
  Given a report with one row
  Then the export button is enabled
`

// assertNothingResolved checks that a refused resolve changed nothing: the
// question still holds the story, which still has its two agreed scenarios.
func assertNothingResolved(t *testing.T, dir, story, questionID string) {
	t.Helper()
	var next nextOut
	mustRun(t, dir, "", &next, "next", "--story", story)
	if next.Question == nil || next.Question.ID != questionID {
		t.Fatalf("question should still be open, next = %#v", next)
	}
	var shown storyOut
	mustRun(t, dir, "", &shown, "story", "show", story)
	if len(shown.Scenarios) != 2 {
		t.Fatalf("scenarios = %d, want 2", len(shown.Scenarios))
	}
}

func TestAskingAQuestion(t *testing.T) {
	t.Run("an open question holds the story's queue", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What should happen with an empty report?")
		if q.Status != "open" || q.StoryID != story || len(q.ID) != questionIDLen {
			t.Fatalf("question = %#v", q)
		}
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Scenario != nil || next.Question == nil || next.Question.ID != q.ID {
			t.Fatalf("next = %#v, want the question and no scenario", next)
		}
	})

	t.Run("with several open questions, next hands back the oldest", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		first := ask(t, dir, story, "What about empty reports?")
		ask(t, dir, story, "What about very wide reports?")
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Question == nil || next.Question.ID != first.ID {
			t.Fatalf("next = %#v, want %s", next.Question, first.ID)
		}
	})

	t.Run("the story shows its open questions without their rounds", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "let's try disabling the button")
		var shown struct {
			Questions []map[string]any `json:"questions"`
		}
		mustRun(t, dir, "", &shown, "story", "show", story)
		if len(shown.Questions) != 1 || shown.Questions[0]["id"] != q.ID {
			t.Fatalf("questions = %#v", shown.Questions)
		}
		if _, ok := shown.Questions[0]["rounds"]; ok {
			t.Fatalf("story view should not include rounds: %#v", shown.Questions[0])
		}
	})

	t.Run("asking on an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "question", "ask", "--story", "nope00", "--body", "x"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("a question needs some text", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		if code := errorCode(t, dir, "", "question", "ask", "--story", story); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}

func TestAnsweringAQuestion(t *testing.T) {
	t.Run("each answer is kept as a round, in order", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "let's try hiding the button")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "no - people think export broke. Disable it instead")

		var shown questionOut
		mustRun(t, dir, "", &shown, "question", "show", q.ID)
		if len(shown.Rounds) != 2 || shown.Rounds[0].Answer != "let's try hiding the button" ||
			shown.Rounds[1].Answer != "no - people think export broke. Disable it instead" {
			t.Fatalf("rounds = %#v", shown.Rounds)
		}
		if shown.Status != "open" {
			t.Fatalf("status = %q, answering alone must not resolve it", shown.Status)
		}
	})

	t.Run("answering an unknown question is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "question", "answer", "nope00", "--body", "x"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

func TestResolvingAQuestion(t *testing.T) {
	t.Run("as new behaviour, it adds a scenario that remembers the question", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "disable the button")

		var resolved questionOut
		mustRun(t, dir, emptyReportScenario, &resolved, "question", "resolve", q.ID,
			"--note", "Empty reports disable the button; hiding it made people think export had broken.", "--scenarios", "-")
		if resolved.Status != "resolved" || len(resolved.Scenarios) != 1 {
			t.Fatalf("resolved = %#v", resolved)
		}
		if resolved.Scenarios[0].Status != "open" {
			t.Fatalf("without a test, the new scenario should arrive open: %#v", resolved.Scenarios[0])
		}

		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		last := shown.Scenarios[len(shown.Scenarios)-1]
		if len(shown.Scenarios) != 3 || last.Title != "Exporting an empty report" || last.QuestionID != q.ID {
			t.Fatalf("scenarios = %#v", shown.Scenarios)
		}
		if shown.Scenarios[0].QuestionID != "" {
			t.Fatalf("an agreed scenario should have no question: %#v", shown.Scenarios[0])
		}
		if len(shown.Questions) != 0 {
			t.Fatalf("resolved questions should leave the story view: %#v", shown.Questions)
		}

		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Question != nil || next.Scenario == nil || next.Scenario.ID != ids[0] {
			t.Fatalf("next = %#v, want the queue to carry on", next)
		}
	})

	t.Run("as a tweak, it adds nothing to the spec but keeps the note and rounds", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "Does the export menu look right?")
		mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "right-align it")
		mustRun(t, dir, "", nil, "question", "resolve", q.ID, "--note", "Right-aligned the export menu.")

		var shown questionOut
		mustRun(t, dir, "", &shown, "question", "show", q.ID)
		if shown.Status != "resolved" || shown.Note != "Right-aligned the export menu." || len(shown.Rounds) != 1 {
			t.Fatalf("question = %#v", shown)
		}
		var story_ storyOut
		mustRun(t, dir, "", &story_, "story", "show", story)
		if len(story_.Scenarios) != 2 {
			t.Fatalf("a tweak should add no scenarios, got %d", len(story_.Scenarios))
		}
	})

	t.Run("only one of note and scenarios can read stdin", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		if code := errorCode(t, dir, emptyReportScenario, "question", "resolve", q.ID, "--note", "-", "--scenarios", "-"); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})

	t.Run("it needs a note", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		if code := errorCode(t, dir, "", "question", "resolve", q.ID); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})

	t.Run("unsupported Gherkin leaves the question open and adds nothing", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		if code := errorCode(t, dir, "@slow\n"+emptyReportScenario, "question", "resolve", q.ID, "--note", "x", "--scenarios", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
		assertNothingResolved(t, dir, story, q.ID)
	})

	t.Run("a resolved question can't be resolved or answered again", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		q := ask(t, dir, story, "What about empty reports?")
		mustRun(t, dir, "", nil, "question", "resolve", q.ID, "--note", "done")
		if code := errorCode(t, dir, "", "question", "resolve", q.ID, "--note", "again"); code != "conflict" {
			t.Fatalf("resolve again: code = %q, want conflict", code)
		}
		if code := errorCode(t, dir, "", "question", "answer", q.ID, "--body", "more"); code != "conflict" {
			t.Fatalf("answer after resolve: code = %q, want conflict", code)
		}
	})
}

// Inside a question's rounds the agent writes the test first (ant aat-cwm6b),
// so by the time the user says yes, the new scenario is already built.
func TestResolvingAQuestionWithTheTestThatProvesIt(t *testing.T) {
	setup := func(t *testing.T) (string, string, []string, questionOut) {
		t.Helper()
		dir, story, ids := storyWithScenarios(t)
		os.WriteFile(filepath.Join(dir, "tests", "Feature", "EmptyReportTest.php"),
			[]byte("<?php\nit('disables export for an empty report', function () {});\n"), 0o644)
		return dir, story, ids, ask(t, dir, story, "What about empty reports?")
	}
	withTest := []string{"--test-file", "tests/Feature/EmptyReportTest.php", "--test", "disables export for an empty report"}

	t.Run("the new scenario arrives closed with its test, and next moves past it", func(t *testing.T) {
		dir, story, ids, q := setup(t)
		var resolved questionOut
		mustRun(t, dir, emptyReportScenario, &resolved, append([]string{"question", "resolve", q.ID,
			"--note", "Empty reports disable the button.", "--scenarios", "-"}, withTest...)...)
		if len(resolved.Scenarios) != 1 {
			t.Fatalf("resolved = %#v", resolved)
		}
		sc := resolved.Scenarios[0]
		if sc.Status != "closed" || sc.TestFile != "tests/Feature/EmptyReportTest.php" || sc.Test != "disables export for an empty report" {
			t.Fatalf("scenario = %#v", sc)
		}
		if resolved.Check != "found the test name in the file" {
			t.Fatalf("check = %q", resolved.Check)
		}

		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		last := shown.Scenarios[len(shown.Scenarios)-1]
		if last.ID != sc.ID || last.Status != "closed" || last.TestFile != "tests/Feature/EmptyReportTest.php" {
			t.Fatalf("story's last scenario = %#v", last)
		}

		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		out, _, code := run(t, dir, "", "next", "--story", story)
		if code != 0 || strings.TrimSpace(out) != `{"scenario":null}` {
			t.Fatalf("exit %d, out %q, want nothing next", code, out)
		}
	})

	t.Run("reusing another scenario's test resolves with a warning", func(t *testing.T) {
		dir, _, ids, q := setup(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		var resolved questionOut
		mustRun(t, dir, emptyReportScenario, &resolved, "question", "resolve", q.ID, "--note", "x", "--scenarios", "-",
			"--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if len(resolved.Warnings) != 1 || !strings.Contains(resolved.Warnings[0], ids[0]) {
			t.Fatalf("warnings = %#v, want one naming %s", resolved.Warnings, ids[0])
		}
	})

	refusals := []struct {
		name, stdin string
		flags       []string
		want        string
	}{
		{"the test name isn't in the file", emptyReportScenario,
			[]string{"--scenarios", "-", "--test-file", "tests/Feature/EmptyReportTest.php", "--test", "hides export for an empty report"}, "validation"},
		{"the test file is outside the project", emptyReportScenario,
			[]string{"--scenarios", "-", "--test-file", "../elsewhere.php", "--test", "x"}, "validation"},
		{"there is a test file but no test name", emptyReportScenario,
			[]string{"--scenarios", "-", "--test-file", "tests/Feature/EmptyReportTest.php"}, "usage"},
		{"there is a test but no new scenario", "", withTest, "usage"},
		{"one test is given for two new scenarios", twoNewScenarios,
			append([]string{"--scenarios", "-"}, withTest...), "validation"},
	}
	for _, c := range refusals {
		t.Run("it is refused when "+c.name+", and nothing changes", func(t *testing.T) {
			dir, story, _, q := setup(t)
			args := append([]string{"question", "resolve", q.ID, "--note", "x"}, c.flags...)
			if code := errorCode(t, dir, c.stdin, args...); code != c.want {
				t.Fatalf("code = %q, want %q", code, c.want)
			}
			assertNothingResolved(t, dir, story, q.ID)
		})
	}
}

// A check-in is planned when the work is agreed, so even an agent deep in
// the build stops at that point and shows the user (foundation, aat-AkRXV).
func TestPlanningACheckIn(t *testing.T) {
	// setup is storyWithScenarios plus a check-in planned after its two
	// scenarios, and a third scenario after the check-in.
	setup := func(t *testing.T) (string, string, []string, questionOut) {
		t.Helper()
		dir, story, ids := storyWithScenarios(t)
		var planned questionOut
		mustRun(t, dir, "", &planned, "question", "plan", "--story", story, "--body", "Show the user the export menu. Does it look right?")
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, emptyReportScenario, &added, "scenario", "add", "--story", story, "--body", "-")
		return dir, story, append(ids, added.Scenarios[0].ID), planned
	}

	t.Run("it waits for the scenarios before it, and holds back the ones after it", func(t *testing.T) {
		dir, story, ids, planned := setup(t)
		if planned.Status != "open" || planned.Position == 0 {
			t.Fatalf("planned = %#v", planned)
		}
		if got := nextID(t, dir, story); got != ids[0] {
			t.Fatalf("next = %q, want the first scenario %s", got, ids[0])
		}
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		if got := nextID(t, dir, story); got != planned.ID {
			t.Fatalf("next = %q, want the check-in %s", got, planned.ID)
		}
		mustRun(t, dir, "", nil, "question", "resolve", planned.ID, "--note", "The user is happy with the menu.")
		if got := nextID(t, dir, story); got != ids[2] {
			t.Fatalf("next = %q, want the scenario after the check-in, %s", got, ids[2])
		}
	})

	t.Run("a question asked mid-build still comes first", func(t *testing.T) {
		dir, story, _, _ := setup(t)
		asked := ask(t, dir, story, "What about very wide reports?")
		if got := nextID(t, dir, story); got != asked.ID {
			t.Fatalf("next = %q, want the asked question %s", got, asked.ID)
		}
	})

	t.Run("the story lists the check-in in its place among the scenarios", func(t *testing.T) {
		dir, story, ids, planned := setup(t)
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if got, want := stepIDs(shown.Steps), strings.Join([]string{ids[0], ids[1], planned.ID, ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
		if q := shown.Steps[2].Question; q.Status != "open" || q.Position != planned.Position {
			t.Fatalf("check-in = %#v", q)
		}
		if len(shown.Questions) != 0 {
			t.Fatalf("questions should be the ones asked mid-build only: %#v", shown.Questions)
		}
	})

	t.Run("once resolved, the story still shows the check-in in its place, with its note", func(t *testing.T) {
		dir, story, ids, planned := setup(t)
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		mustRun(t, dir, "", nil, "question", "answer", planned.ID, "--body", "Make the menu wider.")
		mustRun(t, dir, "", nil, "question", "resolve", planned.ID, "--note", "The user is happy with the wider menu.")
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if got, want := stepIDs(shown.Steps), strings.Join([]string{ids[0], ids[1], planned.ID, ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
		got := shown.Steps[2].Question
		if got.ID != planned.ID || got.Position != planned.Position || got.Status != "resolved" ||
			got.Note != "The user is happy with the wider menu." || got.ResolvedAt == "" || len(got.Rounds) != 0 {
			t.Fatalf("check-in = %#v", got)
		}
	})

	t.Run("a question asked mid-build isn't a step, and leaves the questions list once resolved", func(t *testing.T) {
		dir, story, ids, planned := setup(t)
		asked := ask(t, dir, story, "What about very wide reports?")
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if len(shown.Questions) != 1 || shown.Questions[0].ID != asked.ID {
			t.Fatalf("questions = %#v", shown.Questions)
		}
		mustRun(t, dir, "", nil, "question", "resolve", asked.ID, "--note", "Same as any report.")
		mustRun(t, dir, "", &shown, "story", "show", story)
		if got, want := stepIDs(shown.Steps), strings.Join([]string{ids[0], ids[1], planned.ID, ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("planning on an unknown story is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "question", "plan", "--story", "nope00", "--body", "x"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("a check-in needs some text", func(t *testing.T) {
		dir, story, _ := storyWithScenarios(t)
		if code := errorCode(t, dir, "", "question", "plan", "--story", story); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}
