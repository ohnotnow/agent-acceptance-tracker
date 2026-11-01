package aat

import (
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
	Scenarios []scenarioOut `json:"scenarios"`
}

type nextOut struct {
	Scenario *scenarioOut `json:"scenario"`
	Question *questionOut `json:"question"`
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

		var shown struct {
			Scenarios []struct {
				ID         string `json:"id"`
				Title      string `json:"title"`
				QuestionID string `json:"question_id"`
			} `json:"scenarios"`
			Questions []questionOut `json:"questions"`
		}
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
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", story)
		if next.Question == nil || next.Question.ID != q.ID {
			t.Fatalf("question should still be open, next = %#v", next)
		}
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		if len(shown.Scenarios) != 2 {
			t.Fatalf("scenarios = %d, want 2", len(shown.Scenarios))
		}
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
