package aat

import (
	"strings"
	"testing"
)

// A story is often tidied up after it is agreed (a pre-flight check finds a
// vague scenario, a missing one, a check-in that needs more detail). Until a
// step is built or settled it is a draft: it can be edited, moved, or have
// new work put before it. Once a scenario is closed with a test, the test was
// written against its words, so changes go through questions and
// replacements as before.

// amendSetup is storyWithScenarios plus a check-in planned after its two
// scenarios and a third scenario after the check-in. It returns the dir, the
// story, the three scenario ids and the check-in.
func amendSetup(t *testing.T) (string, string, []string, questionOut) {
	t.Helper()
	dir, story, ids := storyWithScenarios(t)
	var planned questionOut
	mustRun(t, dir, "", &planned, "question", "plan", "--story", story, "--body", "Show the user the export menu.")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, emptyReportScenario, &added, "scenario", "add", "--story", story, "--body", "-")
	return dir, story, append(ids, added.Scenarios[0].ID), planned
}

func storySteps(t *testing.T, dir, story string) string {
	t.Helper()
	var shown storyOut
	mustRun(t, dir, "", &shown, "story", "show", story)
	return stepIDs(shown.Steps)
}

func closeFirst(t *testing.T, dir, id string) {
	t.Helper()
	mustRun(t, dir, "", nil, "close", id, "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
}

const exportAsXlsxReworded = `Scenario: Exporting as xlsx keeps every row
  Given a report with 3 rows
  When I export it as xlsx
  Then the xlsx file has the same 3 rows as the csv export
`

func TestEditingAScenarioBeforeItIsBuilt(t *testing.T) {
	t.Run("an open scenario is changed in place, keeping its id and its place", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		var edited scenarioOut
		mustRun(t, dir, exportAsXlsxReworded, &edited, "scenario", "edit", ids[0], "--body", "-")
		if edited.ID != ids[0] || edited.Title != "Exporting as xlsx keeps every row" || len(edited.Lines) != 3 || edited.Status != "open" {
			t.Fatalf("edited = %#v", edited)
		}
		if edited.Lines[2].Keyword != "Then" || edited.Lines[2].Text != "the xlsx file has the same 3 rows as the csv export" {
			t.Fatalf("lines = %#v", edited.Lines)
		}
		if got, want := storySteps(t, dir, story), strings.Join([]string{ids[0], ids[1], planned.ID, ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("a closed scenario is refused, because its test was written against its words", func(t *testing.T) {
		dir, _, ids, _ := amendSetup(t)
		closeFirst(t, dir, ids[0])
		if code := errorCode(t, dir, exportAsXlsxReworded, "scenario", "edit", ids[0], "--body", "-"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		var sc scenarioOut
		mustRun(t, dir, "", &sc, "scenario", "show", ids[0])
		if sc.Title != "Exporting as xlsx" {
			t.Fatalf("a refused edit changed the scenario: %#v", sc)
		}
	})

	t.Run("a replaced scenario is refused", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		mustRun(t, dir, emptyReportScenario, nil, "scenario", "replace", ids[1], "--story", story, "--reason", "Simpler.", "--body", "-")
		if code := errorCode(t, dir, exportAsXlsxReworded, "scenario", "edit", ids[1], "--body", "-"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("the body must be exactly one scenario", func(t *testing.T) {
		dir, _, ids, _ := amendSetup(t)
		if code := errorCode(t, dir, twoScenarios, "scenario", "edit", ids[0], "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("an unknown scenario is not found", func(t *testing.T) {
		dir, _, _, _ := amendSetup(t)
		if code := errorCode(t, dir, exportAsXlsxReworded, "scenario", "edit", "NoSuchSc", "--body", "-"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

// Once next has handed a scenario to the builder, the builder may already
// have a failing test against its words, so it is no longer a draft.
func TestAScenarioHandedOutIsNoLongerADraft(t *testing.T) {
	t.Run("next stamps the scenario it hands out", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Scenario == nil || next.Scenario.ID != ids[0] || next.Scenario.HandedOutAt == "" {
			t.Fatalf("next = %#v", next.Scenario)
		}
		var sc scenarioOut
		mustRun(t, dir, "", &sc, "scenario", "show", ids[1])
		if sc.HandedOutAt != "" {
			t.Fatalf("a scenario next hasn't reached is stamped: %#v", sc)
		}
	})

	t.Run("it can't be edited, deleted, moved or have work put before it", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		nextID(t, dir, story)
		before := storySteps(t, dir, story)
		for _, args := range [][]string{
			{"scenario", "edit", ids[0], "--body", exportAsXlsxReworded},
			{"scenario", "delete", ids[0]},
			{"scenario", "move", ids[0], "--before", planned.ID},
			{"scenario", "add", "--story", story, "--before", ids[0], "--body", emptyReportScenario},
		} {
			if code := errorCode(t, dir, "", args...); code != "conflict" {
				t.Fatalf("aat %v: code = %q, want conflict", args, code)
			}
		}
		if got := storySteps(t, dir, story); got != before {
			t.Fatalf("refused changes changed the story: %s, was %s", got, before)
		}
	})

	t.Run("the scenarios it hasn't reached are still drafts", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		nextID(t, dir, story)
		mustRun(t, dir, exportAsXlsxReworded, nil, "scenario", "edit", ids[1], "--body", "-")
	})

	t.Run("refusing to accept an unfinished story hands nothing out", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "story", "accept", story, "--note", "Looks good."); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		mustRun(t, dir, exportAsXlsxReworded, nil, "scenario", "edit", ids[0], "--body", "-")
	})
}

func TestEditingAQuestionBeforeItIsAnswered(t *testing.T) {
	t.Run("an unanswered check-in's text is changed in place", func(t *testing.T) {
		dir, _, _, planned := amendSetup(t)
		var edited questionOut
		mustRun(t, dir, "", &edited, "question", "edit", planned.ID, "--body", "Show the user the export menu on their phone.")
		if edited.ID != planned.ID || edited.Text != "Show the user the export menu on their phone." || edited.Position != planned.Position {
			t.Fatalf("edited = %#v", edited)
		}
	})

	t.Run("a question asked mid-build can be edited too, before it is answered", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		asked := ask(t, dir, story, "What about wide reports?")
		var edited questionOut
		mustRun(t, dir, "", &edited, "question", "edit", asked.ID, "--body", "What about reports wider than the screen?")
		if edited.Text != "What about reports wider than the screen?" {
			t.Fatalf("edited = %#v", edited)
		}
	})

	t.Run("once the user has answered a round, the text is fixed", func(t *testing.T) {
		dir, _, _, planned := amendSetup(t)
		mustRun(t, dir, "", nil, "question", "answer", planned.ID, "--body", "Make it wider.")
		if code := errorCode(t, dir, "", "question", "edit", planned.ID, "--body", "Something else."); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("a resolved question is refused", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		asked := ask(t, dir, story, "What about wide reports?")
		mustRun(t, dir, "", nil, "question", "resolve", asked.ID, "--note", "Not needed.")
		if code := errorCode(t, dir, "", "question", "edit", asked.ID, "--body", "Something else."); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("an empty body is refused", func(t *testing.T) {
		dir, _, _, planned := amendSetup(t)
		if code := errorCode(t, dir, "", "question", "edit", planned.ID, "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})
}

func TestDeletingADraftScenario(t *testing.T) {
	t.Run("an open scenario is deleted, and the story carries on without it", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		mustRun(t, dir, "", nil, "scenario", "delete", ids[0])
		if got, want := storySteps(t, dir, story), strings.Join([]string{ids[1], planned.ID, ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
		if got := nextID(t, dir, story); got != ids[1] {
			t.Fatalf("next = %q, want %s", got, ids[1])
		}
		if code := errorCode(t, dir, "", "scenario", "show", ids[0]); code != "not_found" {
			t.Fatalf("show after delete: code = %q, want not_found", code)
		}
	})

	t.Run("a closed scenario is refused", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		closeFirst(t, dir, ids[0])
		before := storySteps(t, dir, story)
		if code := errorCode(t, dir, "", "scenario", "delete", ids[0]); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		if got := storySteps(t, dir, story); got != before {
			t.Fatalf("a refused delete changed the story: %s, was %s", got, before)
		}
	})

	t.Run("a scenario that replaces an earlier rule is refused, so the old rule keeps its link", func(t *testing.T) {
		dir, _, _, _, replacement := postcodeReplaced(t)
		if code := errorCode(t, dir, "", "scenario", "delete", replacement.ID); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("a scenario that came out of the user's answer to a question is refused", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		asked := ask(t, dir, story, "What about empty reports?")
		var resolved questionOut
		mustRun(t, dir, emptyReportScenario, &resolved, "question", "resolve", asked.ID, "--note", "Disable the button.", "--scenarios", "-")
		if code := errorCode(t, dir, "", "scenario", "delete", resolved.Scenarios[0].ID); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("an unknown scenario is not found", func(t *testing.T) {
		dir, _, _, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "scenario", "delete", "NoSuchSc"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

// A story's title and description guide the builder; no test is written
// against them, so they can change until the user accepts the story.
func TestEditingAStory(t *testing.T) {
	t.Run("the description and title change, even mid-build", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		closeFirst(t, dir, ids[0])
		mustRun(t, dir, "Why xlsx, and the column limits.", nil, "story", "edit", story, "--description", "-")
		mustRun(t, dir, "", nil, "story", "edit", story, "--title", "Add xlsx export")
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if shown.Description != "Why xlsx, and the column limits." || shown.Title != "Add xlsx export" {
			t.Fatalf("story = %q / %q", shown.Title, shown.Description)
		}
	})

	t.Run("an empty title or description is refused", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "story", "edit", story, "--description", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
		if code := errorCode(t, dir, "", "story", "edit", story, "--title", " "); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("something to change is required", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "story", "edit", story); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})

	t.Run("an accepted story is refused", func(t *testing.T) {
		dir := newProject(t)
		s := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Nothing to build")
		mustRun(t, dir, "", nil, "story", "accept", s.ID, "--note", "Fine.")
		if code := errorCode(t, dir, "", "story", "edit", s.ID, "--title", "Renamed"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})
}

func TestPuttingWorkBeforeAStep(t *testing.T) {
	t.Run("scenarios added before a check-in go in front of it, in order", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, twoNewScenarios, &added, "scenario", "add", "--story", story, "--before", planned.ID, "--body", "-")
		want := strings.Join([]string{ids[0], ids[1], added.Scenarios[0].ID, added.Scenarios[1].ID, planned.ID, ids[2]}, " ")
		if got := storySteps(t, dir, story); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("next hands out an inserted scenario in its place", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, emptyReportScenario, &added, "scenario", "add", "--story", story, "--before", planned.ID, "--body", "-")
		closeFirst(t, dir, ids[0])
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		if got := nextID(t, dir, story); got != added.Scenarios[0].ID {
			t.Fatalf("next = %q, want the inserted scenario %s before the check-in", got, added.Scenarios[0].ID)
		}
	})

	t.Run("a check-in can be planned before a scenario", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		var early questionOut
		mustRun(t, dir, "", &early, "question", "plan", "--story", story, "--before", ids[1], "--body", "Show the user the xlsx file.")
		want := strings.Join([]string{ids[0], early.ID, ids[1], planned.ID, ids[2]}, " ")
		if got := storySteps(t, dir, story); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("nothing goes before a scenario already closed", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		closeFirst(t, dir, ids[0])
		before := storySteps(t, dir, story)
		if code := errorCode(t, dir, emptyReportScenario, "scenario", "add", "--story", story, "--before", ids[0], "--body", "-"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		if got := storySteps(t, dir, story); got != before {
			t.Fatalf("a refused add changed the story: %s, was %s", got, before)
		}
	})

	t.Run("nothing goes before a check-in already settled", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		closeFirst(t, dir, ids[0])
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		mustRun(t, dir, "", nil, "question", "resolve", planned.ID, "--note", "Fine.")
		if code := errorCode(t, dir, "", "question", "plan", "--story", story, "--before", planned.ID, "--body", "Another look."); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("the step must be in the same story", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		other := addStory(t, dir, addFeature(t, dir, "Importing").ID, "Import csv")
		if code := errorCode(t, dir, emptyReportScenario, "scenario", "add", "--story", other.ID, "--before", nextID(t, dir, story), "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("a question asked mid-build has no place to go before", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		asked := ask(t, dir, story, "What about wide reports?")
		if code := errorCode(t, dir, emptyReportScenario, "scenario", "add", "--story", story, "--before", asked.ID, "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("an unknown step is not found", func(t *testing.T) {
		dir, story, _, _ := amendSetup(t)
		if code := errorCode(t, dir, emptyReportScenario, "scenario", "add", "--story", story, "--before", "NoSuchSc", "--body", "-"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})

	t.Run("asking mid-build takes no --before, since an asked question has no place", func(t *testing.T) {
		dir, story, ids, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "question", "ask", "--story", story, "--before", ids[0], "--body", "Hmm?"); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}

func TestMovingAStep(t *testing.T) {
	t.Run("a scenario moves in front of a check-in", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		mustRun(t, dir, "", nil, "scenario", "move", ids[2], "--before", planned.ID)
		if got, want := storySteps(t, dir, story), strings.Join([]string{ids[0], ids[1], ids[2], planned.ID}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("a check-in moves in front of a scenario", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		mustRun(t, dir, "", nil, "question", "move", planned.ID, "--before", ids[1])
		if got, want := storySteps(t, dir, story), strings.Join([]string{ids[0], planned.ID, ids[1], ids[2]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("moves can be repeated without the order going wrong", func(t *testing.T) {
		dir, story, ids, planned := amendSetup(t)
		mustRun(t, dir, "", nil, "scenario", "move", ids[2], "--before", ids[0])
		mustRun(t, dir, "", nil, "scenario", "move", ids[0], "--before", planned.ID)
		mustRun(t, dir, "", nil, "question", "move", planned.ID, "--before", ids[2])
		if got, want := storySteps(t, dir, story), strings.Join([]string{planned.ID, ids[2], ids[1], ids[0]}, " "); got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
	})

	t.Run("a closed scenario stays where it is", func(t *testing.T) {
		dir, _, ids, planned := amendSetup(t)
		closeFirst(t, dir, ids[0])
		if code := errorCode(t, dir, "", "scenario", "move", ids[0], "--before", planned.ID); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("nothing moves in front of a closed scenario", func(t *testing.T) {
		dir, _, ids, _ := amendSetup(t)
		closeFirst(t, dir, ids[0])
		if code := errorCode(t, dir, "", "scenario", "move", ids[2], "--before", ids[0]); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("a step can't move in front of itself", func(t *testing.T) {
		dir, _, ids, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "scenario", "move", ids[1], "--before", ids[1]); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})

	t.Run("scenario move only moves scenarios, and question move only check-ins", func(t *testing.T) {
		dir, _, ids, planned := amendSetup(t)
		if code := errorCode(t, dir, "", "scenario", "move", planned.ID, "--before", ids[0]); code != "not_found" {
			t.Fatalf("scenario move of a check-in: code = %q, want not_found", code)
		}
		if code := errorCode(t, dir, "", "question", "move", ids[2], "--before", ids[0]); code != "not_found" {
			t.Fatalf("question move of a scenario: code = %q, want not_found", code)
		}
	})

	t.Run("--before is required", func(t *testing.T) {
		dir, _, ids, _ := amendSetup(t)
		if code := errorCode(t, dir, "", "scenario", "move", ids[2]); code != "usage" {
			t.Fatalf("code = %q, want usage", code)
		}
	})
}
