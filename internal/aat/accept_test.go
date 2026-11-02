package aat

import (
	"strings"
	"testing"
)

// The last step of the workflow (foundation aat-AkRXV): once a story is
// finished, the user reviews it and accepts it. Accepting records that the
// story was looked at, when, and a short note.

// finishedStory is storyWithScenarios with both scenarios closed.
func finishedStory(t *testing.T) (string, string) {
	t.Helper()
	dir, story, ids := storyWithScenarios(t)
	mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
	mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
	return dir, story
}

type acceptedOut struct {
	ID         string `json:"id"`
	AcceptedAt string `json:"accepted_at"`
	AcceptNote string `json:"accept_note"`
}

func TestAcceptingAStory(t *testing.T) {
	t.Run("a finished story is accepted with a note, and shows as accepted", func(t *testing.T) {
		dir, story := finishedStory(t)
		var accepted acceptedOut
		mustRun(t, dir, "", &accepted, "story", "accept", story, "--note", "Walked every scenario with its test; happy.")
		if accepted.ID != story || accepted.AcceptedAt == "" || accepted.AcceptNote != "Walked every scenario with its test; happy." {
			t.Fatalf("accept = %#v", accepted)
		}
		var shown acceptedOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if shown != accepted {
			t.Fatalf("story show = %#v, want %#v", shown, accepted)
		}
	})

	t.Run("it is refused while anything is left to do, or without a note", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		if code := errorCode(t, dir, "", "story", "accept", story, "--note", "x"); code != "conflict" {
			t.Fatalf("with open scenarios: code = %q, want conflict", code)
		}
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, dir, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		q := ask(t, dir, story, "Should the export button be green?")
		if code := errorCode(t, dir, "", "story", "accept", story, "--note", "x"); code != "conflict" {
			t.Fatalf("with an open question: code = %q, want conflict", code)
		}
		mustRun(t, dir, "", nil, "question", "resolve", q.ID, "--note", "Left as it is.")
		if code := errorCode(t, dir, "", "story", "accept", story); code != "usage" {
			t.Fatalf("without a note: code = %q, want usage", code)
		}
		mustRun(t, dir, "", nil, "story", "accept", story, "--note", "Happy.")
		if code := errorCode(t, dir, "", "story", "accept", story, "--note", "Again."); code != "conflict" {
			t.Fatalf("twice: code = %q, want conflict", code)
		}
	})

	t.Run("an accepted story takes no more scenarios or questions; later changes need a new story", func(t *testing.T) {
		dir, story := finishedStory(t)
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		mustRun(t, dir, "", nil, "story", "accept", story, "--note", "Happy.")
		for _, args := range [][]string{
			{"scenario", "add", "--story", story, "--body", emptyReportScenario},
			{"question", "ask", "--story", story, "--body", "One more thing?"},
			{"question", "plan", "--story", story, "--body", "Show the user again."},
			{"scenario", "retire", shown.Scenarios[0].ID, "--story", story, "--reason", "x"},
			{"scenario", "replace", shown.Scenarios[0].ID, "--story", story, "--reason", "x", "--body", emptyReportScenario},
		} {
			if code := errorCode(t, dir, "", args...); code != "conflict" {
				t.Fatalf("%s %s: code = %q, want conflict", args[0], args[1], code)
			}
		}
		later := addStory(t, dir, shown.Feature.ID, "Later work")
		mustRun(t, dir, "", nil, "scenario", "retire", shown.Scenarios[0].ID, "--story", later.ID, "--reason", "No longer wanted.")
	})

	t.Run("the export says whether the story was accepted", func(t *testing.T) {
		dir, story := finishedStory(t)
		out, _, _ := run(t, dir, "", "export", story)
		if !strings.Contains(out, "Not accepted yet.") {
			t.Fatalf("export =\n%s", out)
		}
		mustRun(t, dir, "", nil, "story", "accept", story, "--note", "Walked every scenario; happy.")
		out, _, _ = run(t, dir, "", "export", story)
		if !strings.Contains(out, "Accepted on ") || !strings.Contains(out, "Walked every scenario; happy.") {
			t.Fatalf("export =\n%s", out)
		}
	})
}
