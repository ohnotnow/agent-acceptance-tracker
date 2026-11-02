package aat

import (
	"strings"
	"testing"
)

// Corrections (in the spirit of ant's edit): a recorded answer or resolve
// note can be put right, but visibly. The record keeps what it said before,
// why it changed, and when.

type correctionOut struct {
	Target    string `json:"target"`
	Was       string `json:"was"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

type correctedQuestionOut struct {
	ID     string `json:"id"`
	Note   string `json:"note"`
	Rounds []struct {
		Answer string `json:"answer"`
	} `json:"rounds"`
	Corrections []correctionOut `json:"corrections"`
}

// answeredQuestion asks a question on a fresh story and records two answers.
func answeredQuestion(t *testing.T) (string, string, string) {
	t.Helper()
	dir, story, _ := storyWithScenarios(t)
	q := ask(t, dir, story, "Should the export button be green?")
	mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "Maybe.")
	mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "Yes, green. (Agreeing to: every button green.)")
	return dir, story, q.ID
}

func TestCorrectingARecordedAnswer(t *testing.T) {
	t.Run("the answer is put right, and the record shows what it said, why and when", func(t *testing.T) {
		dir, _, q := answeredQuestion(t)
		mustRun(t, dir, "", nil, "question", "correct", q, "--round", "2", "--body", "Yes, green.",
			"--reason", "Removed a summary the coordinator added; not the user's words.")
		var shown correctedQuestionOut
		mustRun(t, dir, "", &shown, "question", "show", q)
		if len(shown.Rounds) != 2 || shown.Rounds[0].Answer != "Maybe." || shown.Rounds[1].Answer != "Yes, green." {
			t.Fatalf("rounds = %#v", shown.Rounds)
		}
		if len(shown.Corrections) != 1 {
			t.Fatalf("corrections = %#v", shown.Corrections)
		}
		c := shown.Corrections[0]
		if c.Target != "round 2" || c.Was != "Yes, green. (Agreeing to: every button green.)" ||
			c.Reason != "Removed a summary the coordinator added; not the user's words." || c.CreatedAt == "" {
			t.Fatalf("correction = %#v", c)
		}
	})

	t.Run("it is refused without a reason, for a round that doesn't exist, or when nothing changes", func(t *testing.T) {
		dir, _, q := answeredQuestion(t)
		if code := errorCode(t, dir, "", "question", "correct", q, "--round", "2", "--body", "Yes, green."); code != "usage" {
			t.Fatalf("without a reason: code = %q, want usage", code)
		}
		if code := errorCode(t, dir, "", "question", "correct", q, "--round", "3", "--body", "x", "--reason", "x"); code != "not_found" {
			t.Fatalf("round 3: code = %q, want not_found", code)
		}
		if code := errorCode(t, dir, "", "question", "correct", q, "--round", "1", "--body", "Maybe.", "--reason", "x"); code != "validation" {
			t.Fatalf("unchanged: code = %q, want validation", code)
		}
		if code := errorCode(t, dir, "", "question", "correct", q, "--body", "x", "--reason", "x"); code != "usage" {
			t.Fatalf("no target: code = %q, want usage", code)
		}
		if code := errorCode(t, dir, "", "question", "correct", q, "--round", "1", "--resolve-note", "--body", "x", "--reason", "x"); code != "usage" {
			t.Fatalf("two targets: code = %q, want usage", code)
		}
		var shown correctedQuestionOut
		mustRun(t, dir, "", &shown, "question", "show", q)
		if len(shown.Corrections) != 0 {
			t.Fatalf("a refused correction changed the record: %#v", shown.Corrections)
		}
	})
}

func TestCorrectingAResolveNote(t *testing.T) {
	t.Run("a resolved question's note can be corrected, even on an accepted story", func(t *testing.T) {
		dir, story, q := answeredQuestion(t)
		mustRun(t, dir, "", nil, "question", "resolve", q, "--note", "The button is keyed to the next stop.")
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", story)
		for _, sc := range shown.Scenarios {
			mustRun(t, dir, "", nil, "close", sc.ID, "--test-file", "tests/Feature/ExportTest.php", "--test", map[string]string{
				"Exporting as xlsx":   "exports a report as xlsx",
				"Choosing the format": "offers csv and xlsx in the export menu",
			}[sc.Title])
		}
		mustRun(t, dir, "", nil, "story", "accept", story, "--note", "Happy.")
		mustRun(t, dir, "", nil, "question", "correct", q, "--resolve-note", "--body", "The confirmation is a Flux modal.",
			"--reason", "A later question replaced this fix.")
		var q2 correctedQuestionOut
		mustRun(t, dir, "", &q2, "question", "show", q)
		if q2.Note != "The confirmation is a Flux modal." || len(q2.Corrections) != 1 ||
			q2.Corrections[0].Target != "note" || q2.Corrections[0].Was != "The button is keyed to the next stop." {
			t.Fatalf("question = %#v", q2)
		}
	})

	t.Run("an open question has no note to correct", func(t *testing.T) {
		dir, _, q := answeredQuestion(t)
		if code := errorCode(t, dir, "", "question", "correct", q, "--resolve-note", "--body", "x", "--reason", "x"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("the review shows each correction with what it replaced", func(t *testing.T) {
		dir, story, q := answeredQuestion(t)
		mustRun(t, dir, "", nil, "question", "correct", q, "--round", "2", "--body", "Yes, green.", "--reason", "Not the user's words.")
		out, _, _ := run(t, dir, "", "export", story)
		if !strings.Contains(out, "2. Yes, green.") || !strings.Contains(out, "Corrected round 2") ||
			!strings.Contains(out, "Not the user's words.") || !strings.Contains(out, "(Agreeing to: every button green.)") {
			t.Fatalf("export =\n%s", out)
		}
	})
}
