package aat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The postcode example from the foundation (aat-AkRXV): a sign-up form
// required a UK postcode, and later it doesn't. A future agent should find
// the current rule, the old one it replaced, and why.

const postcodeRequired = `Scenario: A postcode is required
  Given I am signing up
  When I leave the postcode blank
  Then I am told the postcode is required
`

const postcodeOptional = `Scenario: The postcode can be left blank
  Given I am signing up
  When I leave the postcode blank
  Then my account is created
`

// postcodeExample sets up a "Signing up" feature whose first story agreed
// postcodeRequired and closed it with a test. It returns the project dir,
// the feature id and the postcode scenario id.
func postcodeExample(t *testing.T) (string, string, string) {
	t.Helper()
	dir := newProject(t)
	f := addFeature(t, dir, "Signing up")
	first := addStory(t, dir, f.ID, "Sign-up form")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, postcodeRequired, &added, "scenario", "add", "--story", first.ID, "--body", "-")
	writeTests(t, dir, "it('requires a postcode', fn () => true);")
	mustRun(t, dir, "", nil, "close", added.Scenarios[0].ID, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode")
	return dir, f.ID, added.Scenarios[0].ID
}

// writeTests replaces tests/SignUpTest.php with the given test lines.
func writeTests(t *testing.T, dir string, lines ...string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "tests", "SignUpTest.php"), []byte("<?php\n"+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type featureShowOut struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Scenarios []scenarioOut `json:"scenarios"`
	Retired   []scenarioOut `json:"retired"`
}

func titlesOf(scs []scenarioOut) string {
	var titles []string
	for _, sc := range scs {
		titles = append(titles, sc.Title)
	}
	return strings.Join(titles, " | ")
}

// postcodeReplaced sets up postcodeExample, then a later story that replaces
// the postcode rule. It returns the dir, feature, old scenario, later story
// and the new scenario.
func postcodeReplaced(t *testing.T) (string, string, string, string, scenarioOut) {
	t.Helper()
	dir, feature, old := postcodeExample(t)
	later := addStory(t, dir, feature, "Make the postcode optional")
	var sc scenarioOut
	mustRun(t, dir, postcodeOptional, &sc, "scenario", "replace", old, "--story", later.ID,
		"--reason", "Customers abroad have no UK postcode.", "--body", "-")
	return dir, feature, old, later.ID, sc
}

func TestReplacingAScenario(t *testing.T) {
	replaced := postcodeReplaced

	t.Run("the builder is handed the new rule with the old rule, its test and the reason", func(t *testing.T) {
		dir, _, old, later, sc := replaced(t)
		if sc.Title != "The postcode can be left blank" || sc.Status != "open" {
			t.Fatalf("new scenario = %#v", sc)
		}
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", later)
		r := next.Scenario.Replaces
		if next.Scenario.ID != sc.ID || r == nil {
			t.Fatalf("next = %#v, want the new scenario with what it replaces", next.Scenario)
		}
		if r.ID != old || r.Title != "A postcode is required" || r.TestFile != "tests/SignUpTest.php" ||
			r.Test != "requires a postcode" || r.Reason != "Customers abroad have no UK postcode." || len(r.Lines) != 3 {
			t.Fatalf("replaces = %#v", r)
		}
	})

	t.Run("the old scenario stays in its story, marked replaced, with the reason", func(t *testing.T) {
		dir, _, old, _, sc := replaced(t)
		var shown scenarioOut
		mustRun(t, dir, "", &shown, "scenario", "show", old)
		if shown.Status != "replaced" || shown.ReplacedBy != sc.ID || shown.Reason != "Customers abroad have no UK postcode." ||
			shown.Test != "requires a postcode" {
			t.Fatalf("old scenario = %#v", shown)
		}
	})

	t.Run("the feature view and the published file give only the current rule", func(t *testing.T) {
		dir, feature, _, _, _ := replaced(t)
		var shown featureShowOut
		mustRun(t, dir, "", &shown, "feature", "show", feature)
		if got := titlesOf(shown.Scenarios); got != "The postcode can be left blank" {
			t.Fatalf("current scenarios = %s", got)
		}
		if shown.Scenarios[0].Replaces == nil || shown.Scenarios[0].Replaces.Title != "A postcode is required" {
			t.Fatalf("the current rule should say what it replaced: %#v", shown.Scenarios[0])
		}
		mustRun(t, dir, "", nil, "publish")
		published, _ := os.ReadFile(filepath.Join(dir, "features", "signing_up.feature"))
		if !strings.Contains(string(published), "The postcode can be left blank") || strings.Contains(string(published), "A postcode is required") {
			t.Fatalf("published =\n%s", published)
		}
	})

	t.Run("closing the new rule warns while the old test is still there", func(t *testing.T) {
		dir, _, _, _, sc := replaced(t)
		writeTests(t, dir, "it('requires a postcode', fn () => true);", "it('accepts a blank postcode', fn () => true);")
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", sc.ID, "--test-file", "tests/SignUpTest.php", "--test", "accepts a blank postcode")
		if len(closed.Warnings) != 1 || !strings.Contains(closed.Warnings[0], "requires a postcode") {
			t.Fatalf("warnings = %#v, want one about the old test", closed.Warnings)
		}
	})

	t.Run("closing the new rule is quiet once the old test is gone", func(t *testing.T) {
		dir, _, _, _, sc := replaced(t)
		writeTests(t, dir, "it('accepts a blank postcode', fn () => true);")
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", sc.ID, "--test-file", "tests/SignUpTest.php", "--test", "accepts a blank postcode")
		if len(closed.Warnings) != 0 {
			t.Fatalf("warnings = %#v, want none", closed.Warnings)
		}
	})

	t.Run("a replaced scenario that was never built isn't handed out", func(t *testing.T) {
		dir := newProject(t)
		f := addFeature(t, dir, "Signing up")
		first := addStory(t, dir, f.ID, "Sign-up form")
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, postcodeRequired, &added, "scenario", "add", "--story", first.ID, "--body", "-")
		later := addStory(t, dir, f.ID, "Make the postcode optional")
		mustRun(t, dir, postcodeOptional, nil, "scenario", "replace", added.Scenarios[0].ID, "--story", later.ID, "--reason", "x", "--body", "-")
		if got := nextID(t, dir, first.ID); got != "" {
			t.Fatalf("next = %q, want nothing", got)
		}
	})

	t.Run("a question can settle by replacing a scenario", func(t *testing.T) {
		dir, feature, old := postcodeExample(t)
		later := addStory(t, dir, feature, "Overseas sign-ups")
		q := ask(t, dir, later.ID, "Overseas customers can't enter a UK postcode. Should it be optional?")
		var resolved questionOut
		mustRun(t, dir, postcodeOptional, &resolved, "question", "resolve", q.ID,
			"--note", "Postcode is optional; overseas customers have none.", "--scenarios", "-", "--replaces", old)
		var shown scenarioOut
		mustRun(t, dir, "", &shown, "scenario", "show", old)
		if len(resolved.Scenarios) != 1 || shown.ReplacedBy != resolved.Scenarios[0].ID || shown.Reason != "Postcode is optional; overseas customers have none." {
			t.Fatalf("resolved = %#v, old = %#v", resolved, shown)
		}
	})

	refusals := []struct {
		name, stdin, want string
		args              func(old, later string) []string
	}{
		{"without a reason", postcodeOptional, "usage", func(old, later string) []string {
			return []string{"scenario", "replace", old, "--story", later, "--body", "-"}
		}},
		{"with two new scenarios", postcodeOptional + "\n" + postcodeRequired, "validation", func(old, later string) []string {
			return []string{"scenario", "replace", old, "--story", later, "--reason", "x", "--body", "-"}
		}},
		{"for a scenario that doesn't exist", postcodeOptional, "not_found", func(old, later string) []string {
			return []string{"scenario", "replace", "nope0000", "--story", later, "--reason", "x", "--body", "-"}
		}},
		{"from a story that doesn't exist", postcodeOptional, "not_found", func(old, later string) []string {
			return []string{"scenario", "replace", old, "--story", "nope00", "--reason", "x", "--body", "-"}
		}},
		{"by a question settled without a new scenario", "", "usage", func(old, later string) []string {
			return []string{"question", "resolve", "nope00", "--note", "x", "--replaces", old}
		}},
	}
	for _, c := range refusals {
		t.Run("it is refused "+c.name, func(t *testing.T) {
			dir, feature, old := postcodeExample(t)
			later := addStory(t, dir, feature, "Make the postcode optional")
			if code := errorCode(t, dir, c.stdin, c.args(old, later.ID)...); code != c.want {
				t.Fatalf("code = %q, want %q", code, c.want)
			}
			var shown scenarioOut
			mustRun(t, dir, "", &shown, "scenario", "show", old)
			if shown.Status != "closed" {
				t.Fatalf("the old scenario should be untouched: %#v", shown)
			}
		})
	}

	t.Run("it is refused once the scenario is already replaced", func(t *testing.T) {
		dir, _, old, later, _ := replaced(t)
		if code := errorCode(t, dir, postcodeOptional, "scenario", "replace", old, "--story", later, "--reason", "x", "--body", "-"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("it is refused from a story on another feature", func(t *testing.T) {
		dir, _, old := postcodeExample(t)
		elsewhere := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		if code := errorCode(t, dir, postcodeOptional, "scenario", "replace", old, "--story", elsewhere.ID, "--reason", "x", "--body", "-"); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
	})
}

func TestRetiringAScenario(t *testing.T) {
	retired := func(t *testing.T) (string, string, string, string) {
		t.Helper()
		dir, feature, old := postcodeExample(t)
		later := addStory(t, dir, feature, "Stop asking for a postcode")
		mustRun(t, dir, "", nil, "scenario", "retire", old, "--story", later.ID, "--reason", "We no longer post anything to customers.")
		return dir, feature, old, later.ID
	}

	t.Run("it leaves the current spec but stays in the record with the reason", func(t *testing.T) {
		dir, feature, old, later := retired(t)
		var shown featureShowOut
		mustRun(t, dir, "", &shown, "feature", "show", feature)
		if len(shown.Scenarios) != 0 || len(shown.Retired) != 1 || shown.Retired[0].ID != old ||
			shown.Retired[0].Reason != "We no longer post anything to customers." || shown.Retired[0].RetiredBy != later {
			t.Fatalf("feature = %#v", shown)
		}
	})

	t.Run("the retiring story lists it, so its builder removes the behaviour and its test", func(t *testing.T) {
		dir, _, old, later := retired(t)
		var shown storyOut
		mustRun(t, dir, "", &shown, "story", "show", later)
		if len(shown.Retires) != 1 || shown.Retires[0].ID != old || shown.Retires[0].Test != "requires a postcode" {
			t.Fatalf("retires = %#v", shown.Retires)
		}
	})

	t.Run("next hands it out as work before the story's own scenarios", func(t *testing.T) {
		dir, _, old, later := retired(t)
		mustRun(t, dir, postcodeOptional, nil, "scenario", "add", "--story", later, "--body", "-")
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", later)
		if next.Retirement == nil || next.Retirement.ID != old || next.Retirement.Test != "requires a postcode" ||
			next.Retirement.TestFile != "tests/SignUpTest.php" || next.Scenario != nil {
			t.Fatalf("next = %#v", next)
		}
	})

	t.Run("closing it is refused while its test is still in the file", func(t *testing.T) {
		dir, _, old, later := retired(t)
		if code := errorCode(t, dir, "", "close", old); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
		if got := nextID(t, dir, later); got != old {
			t.Fatalf("next = %q, want the retirement %s still", got, old)
		}
	})

	t.Run("once its test is gone, closing it records the removal and next moves on", func(t *testing.T) {
		dir, _, old, later := retired(t)
		writeTests(t, dir, "it('creates an account', fn () => true);")
		var closed struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			RemovedAt string `json:"removed_at"`
			Check     string `json:"check"`
		}
		mustRun(t, dir, "", &closed, "close", old)
		if closed.ID != old || closed.Status != "retired" || closed.RemovedAt == "" || closed.Check == "" {
			t.Fatalf("close = %#v", closed)
		}
		if got := nextID(t, dir, later); got != "" {
			t.Fatalf("next = %q, want nothing left", got)
		}
		var shown scenarioOut
		mustRun(t, dir, "", &shown, "scenario", "show", old)
		if shown.Status != "retired" || shown.RemovedAt != closed.RemovedAt {
			t.Fatalf("scenario = %#v", shown)
		}
		if code := errorCode(t, dir, "", "close", old); code != "conflict" {
			t.Fatalf("closing twice: code = %q, want conflict", code)
		}
	})

	t.Run("a test file that is gone altogether counts as removed", func(t *testing.T) {
		dir, _, old, _ := retired(t)
		if err := os.Remove(filepath.Join(dir, "tests", "SignUpTest.php")); err != nil {
			t.Fatal(err)
		}
		mustRun(t, dir, "", nil, "close", old)
	})

	t.Run("it is refused without a reason, or once already retired", func(t *testing.T) {
		dir, _, old, later := retired(t)
		if code := errorCode(t, dir, "", "scenario", "retire", old, "--story", later); code != "usage" {
			t.Fatalf("without a reason: code = %q, want usage", code)
		}
		if code := errorCode(t, dir, "", "scenario", "retire", old, "--story", later, "--reason", "x"); code != "conflict" {
			t.Fatalf("again: code = %q, want conflict", code)
		}
	})
}

// Edge cases found by a review of replacing and retiring.
func TestChangingTheSpecEdgeCases(t *testing.T) {
	// openPostcodeRule adds postcodeRequired without building it, plus a
	// later story on the same feature. It returns the dir, the scenario and
	// the later story.
	openPostcodeRule := func(t *testing.T) (string, string, string) {
		t.Helper()
		dir := newProject(t)
		f := addFeature(t, dir, "Signing up")
		first := addStory(t, dir, f.ID, "Sign-up form")
		var added struct {
			Scenarios []scenarioOut `json:"scenarios"`
		}
		mustRun(t, dir, postcodeRequired, &added, "scenario", "add", "--story", first.ID, "--body", "-")
		writeTests(t, dir, "it('requires a postcode', fn () => true);")
		return dir, added.Scenarios[0].ID, addStory(t, dir, f.ID, "Later work").ID
	}

	t.Run("a replaced or retired scenario can't be closed", func(t *testing.T) {
		for _, change := range [][]string{
			{"scenario", "replace", "", "--reason", "x", "--body", "-"},
			{"scenario", "retire", "", "--reason", "x"},
		} {
			dir, old, later := openPostcodeRule(t)
			change[2] = old
			mustRun(t, dir, postcodeOptional, nil, append(change, "--story", later)...)
			if code := errorCode(t, dir, "", "close", old, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode"); code != "conflict" {
				t.Fatalf("%s: code = %q, want conflict", change[1], code)
			}
		}
	})

	t.Run("retiring a scenario that was never built leaves no test to look for", func(t *testing.T) {
		dir, old, later := openPostcodeRule(t)
		mustRun(t, dir, "", nil, "scenario", "retire", old, "--story", later, "--reason", "x")
		mustRun(t, dir, "", nil, "close", old)
	})

	t.Run("retiring a replacement that was never built looks for the old rule's test", func(t *testing.T) {
		dir, feature, _, _, unbuilt := postcodeReplaced(t)
		third := addStory(t, dir, feature, "Stop asking for a postcode")
		mustRun(t, dir, "", nil, "scenario", "retire", unbuilt.ID, "--story", third.ID, "--reason", "We no longer post anything to customers.")
		if code := errorCode(t, dir, "", "close", unbuilt.ID); code != "validation" {
			t.Fatalf("code = %q, want validation while 'requires a postcode' is still in the file", code)
		}
	})

	t.Run("a test can't be named when closing a retirement, nor left out when closing a scenario", func(t *testing.T) {
		dir, old, later := openPostcodeRule(t)
		if code := errorCode(t, dir, "", "close", old); code != "usage" {
			t.Fatalf("current scenario without a test: code = %q, want usage", code)
		}
		mustRun(t, dir, "", nil, "scenario", "retire", old, "--story", later, "--reason", "x")
		if code := errorCode(t, dir, "", "close", old, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode"); code != "conflict" {
			t.Fatalf("retirement with a test: code = %q, want conflict", code)
		}
	})

	t.Run("a retired scenario says retired, and the feature's retired list leaves out replaced ones", func(t *testing.T) {
		dir, old, later := openPostcodeRule(t)
		mustRun(t, dir, "", nil, "scenario", "retire", old, "--story", later, "--reason", "x")
		var shown scenarioOut
		mustRun(t, dir, "", &shown, "scenario", "show", old)
		if shown.Status != "retired" {
			t.Fatalf("status = %q, want retired", shown.Status)
		}
		dir, feature, _, _, _ := postcodeReplaced(t)
		var view featureShowOut
		mustRun(t, dir, "", &view, "feature", "show", feature)
		if len(view.Retired) != 0 {
			t.Fatalf("a replaced scenario isn't retired: %#v", view.Retired)
		}
	})

	t.Run("replacing a replacement that was never built still points at the old test", func(t *testing.T) {
		dir, feature, _, _, unbuilt := postcodeReplaced(t)
		third := addStory(t, dir, feature, "UK addresses only")
		mustRun(t, dir, "Scenario: A postcode is needed for UK addresses\n  Given I am signing up with a UK address\n  When I leave the postcode blank\n  Then I am told the postcode is required\n",
			nil, "scenario", "replace", unbuilt.ID, "--story", third.ID, "--reason", "Only UK addresses have postcodes.", "--body", "-")
		var next nextOut
		mustRun(t, dir, "", &next, "next", "--story", third.ID)
		r := next.Scenario.Replaces
		if r == nil || r.ID != unbuilt.ID || r.Test != "requires a postcode" || r.TestFile != "tests/SignUpTest.php" {
			t.Fatalf("replaces = %#v, want the unbuilt rule with the built one's test", r)
		}
	})

	t.Run("a replacement in the same story takes the old one's place, ahead of a later check-in", func(t *testing.T) {
		dir, story, ids := storyWithScenarios(t)
		var checkIn questionOut
		mustRun(t, dir, "", &checkIn, "question", "plan", "--story", story, "--body", "Show the user the export menu.")
		var replacement scenarioOut
		mustRun(t, dir, "Scenario: Choosing from three formats\n  Given a report\n  When I open the export menu\n  Then I can choose csv, xlsx or pdf\n",
			&replacement, "scenario", "replace", ids[1], "--story", story, "--reason", "pdf too", "--body", "-")
		mustRun(t, dir, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		if got := nextID(t, dir, story); got != replacement.ID {
			t.Fatalf("next = %q, want the replacement %s before the check-in %s", got, replacement.ID, checkIn.ID)
		}
	})

	t.Run("closing the replacement with the old test, changed in place, gives no warning", func(t *testing.T) {
		dir, _, _, _, sc := postcodeReplaced(t)
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", sc.ID, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode")
		if len(closed.Warnings) != 0 {
			t.Fatalf("warnings = %#v, want none", closed.Warnings)
		}
	})

	t.Run("a new test whose name contains the old one's isn't taken for the old test", func(t *testing.T) {
		dir, _, _, _, sc := postcodeReplaced(t)
		writeTests(t, dir, "it('requires a postcode only for UK addresses', fn () => true);")
		var closed closeOut
		mustRun(t, dir, "", &closed, "close", sc.ID, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode only for UK addresses")
		if len(closed.Warnings) != 0 {
			t.Fatalf("warnings = %#v, want none", closed.Warnings)
		}
	})

	t.Run("a question settled by replacing, with its test, warns while the old test is still there", func(t *testing.T) {
		dir, feature, old := postcodeExample(t)
		later := addStory(t, dir, feature, "Overseas sign-ups")
		q := ask(t, dir, later.ID, "Overseas customers can't enter a UK postcode.")
		writeTests(t, dir, "it('requires a postcode', fn () => true);", "it('accepts a blank postcode', fn () => true);")
		var resolved questionOut
		mustRun(t, dir, postcodeOptional, &resolved, "question", "resolve", q.ID, "--note", "Optional now.", "--scenarios", "-",
			"--replaces", old, "--test-file", "tests/SignUpTest.php", "--test", "accepts a blank postcode")
		if len(resolved.Warnings) != 1 || !strings.Contains(resolved.Warnings[0], "requires a postcode") {
			t.Fatalf("warnings = %#v, want one about the old test", resolved.Warnings)
		}
	})

	t.Run("a question can't replace a scenario on another feature", func(t *testing.T) {
		dir, _, old := postcodeExample(t)
		elsewhere := addStory(t, dir, addFeature(t, dir, "Exporting reports").ID, "Add xlsx")
		q := ask(t, dir, elsewhere.ID, "x")
		if code := errorCode(t, dir, postcodeOptional, "question", "resolve", q.ID, "--note", "x", "--scenarios", "-", "--replaces", old); code != "validation" {
			t.Fatalf("code = %q, want validation", code)
		}
		if got := nextID(t, dir, elsewhere.ID); got != q.ID {
			t.Fatalf("the question should still be open, next = %q", got)
		}
	})

	t.Run("the export names the old test for replaced and retired rules", func(t *testing.T) {
		dir, _, _, later, _ := postcodeReplaced(t)
		out, _, _ := run(t, dir, "", "export", later)
		if !strings.Contains(out, "The old rule's test was `requires a postcode` in `tests/SignUpTest.php`") {
			t.Fatalf("export =\n%s", out)
		}
		dir, feature, old := postcodeExample(t)
		retiring := addStory(t, dir, feature, "Stop asking for a postcode")
		mustRun(t, dir, "", nil, "scenario", "retire", old, "--story", retiring.ID, "--reason", "x")
		out, _, _ = run(t, dir, "", "export", retiring.ID)
		if !strings.Contains(out, "## Scenarios this story retires") || !strings.Contains(out, "Its test was `requires a postcode`") ||
			!strings.Contains(out, "Removal not closed yet.") {
			t.Fatalf("export =\n%s", out)
		}
		writeTests(t, dir)
		mustRun(t, dir, "", nil, "close", old)
		out, _, _ = run(t, dir, "", "export", retiring.ID)
		if !strings.Contains(out, "Removed: aat found the test name gone from the file") {
			t.Fatalf("export =\n%s", out)
		}
	})
}
