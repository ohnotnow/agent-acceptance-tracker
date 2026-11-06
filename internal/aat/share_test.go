package aat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sharing a spec between developers (ant aat-GShrC): `publish --yaml` writes
// everything aat holds about a feature to one YAML file, and `import` merges
// such a file into another developer's database.

type shareOut struct {
	Files []struct {
		Path    string `json:"path"`
		Feature string `json:"feature"`
	} `json:"files"`
}

// richFeature builds a "Signing up" feature that uses every kind of record:
// an accepted story whose two scenarios a later story replaces and retires,
// and that later story with a handed-out scenario, a planned check-in, and a
// question asked mid-build, answered twice, corrected and resolved with a new
// scenario, and a draft scenario deleted. It returns the project dir, the feature id and the later story.
func richFeature(t *testing.T) (string, string, string) {
	t.Helper()
	dir := newProject(t)
	f := addFeature(t, dir, "Signing up")
	first := addStory(t, dir, f.ID, "Sign-up form")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, postcodeRequired+"\nScenario: A fax number is asked for\n  Given I am signing up\n  Then I can enter a fax number\n", &added,
		"scenario", "add", "--story", first.ID, "--body", "-")
	writeTests(t, dir, "it('requires a postcode', fn () => true);", "it('asks for a fax number', fn () => true);")
	mustRun(t, dir, "", nil, "close", added.Scenarios[0].ID, "--test-file", "tests/SignUpTest.php", "--test", "requires a postcode")
	mustRun(t, dir, "", nil, "close", added.Scenarios[1].ID, "--test-file", "tests/SignUpTest.php", "--test", "asks for a fax number")
	mustRun(t, dir, "", nil, "story", "accept", first.ID, "--note", "Looks right.")

	later := addStory(t, dir, f.ID, "Make the postcode optional")
	mustRun(t, dir, postcodeOptional, nil, "scenario", "replace", added.Scenarios[0].ID, "--story", later.ID,
		"--reason", "Customers abroad have no UK postcode.", "--body", "-")
	mustRun(t, dir, "", nil, "scenario", "retire", added.Scenarios[1].ID, "--story", later.ID, "--reason", "Nobody has a fax.")
	mustRun(t, dir, "", nil, "question", "plan", "--story", later.ID, "--body", "Does the form look right?")
	writeTests(t, dir, "it('requires a postcode', fn () => true);")
	mustRun(t, dir, "", nil, "close", added.Scenarios[1].ID) // the fax test is gone
	mustRun(t, dir, "", nil, "next", "--story", later.ID)    // hands out the replacement

	var scrap struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, "Scenario: Scrap me\n  Then nothing\n", &scrap, "scenario", "add", "--story", later.ID, "--body", "-")
	mustRun(t, dir, "", nil, "scenario", "delete", scrap.Scenarios[0].ID)

	q := ask(t, dir, later.ID, "What about a postcode typed in lower case?")
	mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "Hmm.")
	mustRun(t, dir, "", nil, "question", "answer", q.ID, "--body", "Accept it\nand show it in capitals.")
	mustRun(t, dir, "", nil, "question", "correct", q.ID, "--round", "1", "--body", "Not sure yet.", "--reason", "Typed in a hurry.")
	mustRun(t, dir, "Scenario: A lower-case postcode is accepted\n  Given I am signing up\n  When I enter \"g12 8qq\"\n  Then my postcode is saved as \"G12 8QQ\"\n", nil,
		"question", "resolve", q.ID, "--note", "Accept it, store it in capitals.", "--scenarios", "-")
	return dir, f.ID, later.ID
}

// shareFile publishes dir's features as YAML and returns the path of the one file.
func shareFile(t *testing.T, dir string) string {
	t.Helper()
	var out shareOut
	mustRun(t, dir, "", &out, "publish", "--yaml")
	if len(out.Files) != 1 {
		t.Fatalf("publish --yaml wrote %#v", out)
	}
	return filepath.Join(dir, out.Files[0].Path)
}

// views is everything aat shows about a feature, its stories and their
// questions, as raw JSON, for comparing two databases.
func views(t *testing.T, dir, feature string) string {
	t.Helper()
	show := func(args ...string) string {
		out, errOut, code := run(t, dir, "", args...)
		if code != 0 {
			t.Fatalf("aat %v: exit %d, stderr: %s", args, code, errOut)
		}
		return out
	}
	var b strings.Builder
	b.WriteString(show("feature", "show", feature))
	var stories struct {
		Scenarios []scenarioOut `json:"scenarios"`
		Retired   []scenarioOut `json:"retired"`
	}
	mustRun(t, dir, "", &stories, "feature", "show", feature)
	seen := map[string]bool{}
	for _, sc := range append(stories.Scenarios, stories.Retired...) {
		var one struct {
			StoryID string `json:"story_id"`
		}
		mustRun(t, dir, "", &one, "scenario", "show", sc.ID)
		if seen[one.StoryID] {
			continue
		}
		seen[one.StoryID] = true
		b.WriteString(show("story", "show", one.StoryID))
		var st storyOut
		mustRun(t, dir, "", &st, "story", "show", one.StoryID)
		for _, step := range st.Steps {
			if step.Question != nil {
				b.WriteString(show("question", "show", step.Question.ID))
			}
		}
		for _, sc := range st.Scenarios {
			if sc.QuestionID != "" {
				b.WriteString(show("question", "show", sc.QuestionID))
			}
			b.WriteString(show("scenario", "show", sc.ID))
		}
	}
	return b.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSharingAFeatureAsYAML(t *testing.T) {
	t.Run("the file goes in features/yaml, named like the .feature file", func(t *testing.T) {
		dir, feature, _ := richFeature(t)
		var out shareOut
		mustRun(t, dir, "", &out, "publish", "--yaml")
		if len(out.Files) != 1 || out.Files[0].Path != "features/yaml/signing_up.yaml" || out.Files[0].Feature != feature {
			t.Fatalf("out = %#v", out)
		}
		if !strings.HasPrefix(readFile(t, filepath.Join(dir, out.Files[0].Path)), "# Written by aat publish --yaml from feature "+feature+".") {
			t.Fatal("the file doesn't say aat wrote it")
		}
	})

	t.Run("importing into an empty database rebuilds the same record, and exports the same file", func(t *testing.T) {
		mine, feature, _ := richFeature(t)
		file := shareFile(t, mine)

		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", file)
		if got, want := views(t, theirs, feature), views(t, mine, feature); got != want {
			t.Fatalf("after import:\n%s\nwant:\n%s", got, want)
		}
		if got, want := readFile(t, shareFile(t, theirs)), readFile(t, file); got != want {
			t.Fatalf("re-exported file differs:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("text YAML would otherwise misread comes back as written", func(t *testing.T) {
		mine := newProject(t)
		var f featureOut
		mustRun(t, mine, "", &f, "feature", "add", "--title", "no", "--description", "- not a list\nkey: yes\n[1, 2]")
		s := addStory(t, mine, f.ID, "123")
		mustRun(t, mine, "Scenario: true\n  Given null\n  Then \"~\" and 'x' and: y\n", nil, "scenario", "add", "--story", s.ID, "--body", "-")
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		if got, want := views(t, theirs, f.ID), views(t, mine, f.ID); got != want {
			t.Fatalf("after import:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("importing the same file twice changes nothing", func(t *testing.T) {
		mine, feature, _ := richFeature(t)
		before := views(t, mine, feature)
		mustRun(t, mine, "", nil, "import", shareFile(t, mine))
		if got := views(t, mine, feature); got != before {
			t.Fatalf("after re-import:\n%s\nwant:\n%s", got, before)
		}
	})

	t.Run("a second import after the story has moved on merges the progress", func(t *testing.T) {
		mine, story, ids := storyWithScenarios(t)
		q := ask(t, mine, story, "Green or blue?")
		var shown storyOut
		mustRun(t, mine, "", &shown, "story", "show", story)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))

		mustRun(t, mine, "", nil, "question", "answer", q.ID, "--body", "Green.")
		mustRun(t, mine, "", nil, "question", "resolve", q.ID, "--note", "Green it is.")
		mustRun(t, mine, "", nil, "next", "--story", story)
		mustRun(t, mine, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, mine, "", nil, "close", ids[1], "--test-file", "tests/Feature/ExportTest.php", "--test", "offers csv and xlsx in the export menu")
		mustRun(t, mine, "", nil, "story", "accept", story, "--note", "Good.")
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))

		if got, want := views(t, theirs, shown.Feature.ID), views(t, mine, shown.Feature.ID); got != want {
			t.Fatalf("after second import:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("an older file doesn't undo progress already made", func(t *testing.T) {
		mine, _, ids := storyWithScenarios(t)
		old := shareFile(t, mine)
		oldCopy := filepath.Join(t.TempDir(), "old.yaml")
		os.WriteFile(oldCopy, []byte(readFile(t, old)), 0o644)
		mustRun(t, mine, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		mustRun(t, mine, "", nil, "import", oldCopy)
		var sc scenarioOut
		mustRun(t, mine, "", &sc, "scenario", "show", ids[0])
		if sc.Status != "closed" {
			t.Fatalf("status = %q", sc.Status)
		}
	})

	t.Run("a later replacement of an accepted story's scenario merges", func(t *testing.T) {
		mine, feature, old := postcodeExample(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		later := addStory(t, mine, feature, "Make the postcode optional")
		mustRun(t, mine, postcodeOptional, nil, "scenario", "replace", old, "--story", later.ID,
			"--reason", "Customers abroad have no UK postcode.", "--body", "-")
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		if got, want := views(t, theirs, feature), views(t, mine, feature); got != want {
			t.Fatalf("after import:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("a correction to an answer merges", func(t *testing.T) {
		mine, _, q := answeredQuestion(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, mine, "", nil, "question", "correct", q, "--round", "2", "--body", "Yes, green.", "--reason", "Overreached.")
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		var got correctedQuestionOut
		mustRun(t, theirs, "", &got, "question", "show", q)
		if got.Rounds[1].Answer != "Yes, green." || len(got.Corrections) != 1 || got.Corrections[0].Reason != "Overreached." {
			t.Fatalf("question = %#v", got)
		}
	})
}

func TestSharingADeletedDraft(t *testing.T) {
	t.Run("a draft deleted on one side is deleted on the other", func(t *testing.T) {
		mine, story, ids := storyWithScenarios(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, mine, "", nil, "scenario", "delete", ids[1])
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		if code := errorCode(t, theirs, "", "scenario", "show", ids[1]); code != "not_found" {
			t.Fatalf("code = %q", code)
		}
		var shown storyOut
		mustRun(t, mine, "", &shown, "story", "show", story)
		if got, want := views(t, theirs, shown.Feature.ID), views(t, mine, shown.Feature.ID); got != want {
			t.Fatalf("after import:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("an older file doesn't bring a deleted draft back", func(t *testing.T) {
		mine, _, ids := storyWithScenarios(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, mine, "", nil, "scenario", "delete", ids[1])
		mustRun(t, mine, "", nil, "import", shareFile(t, theirs))
		if code := errorCode(t, mine, "", "scenario", "show", ids[1]); code != "not_found" {
			t.Fatalf("code = %q", code)
		}
	})

	t.Run("a draft deleted on one side but handed out on the other clashes", func(t *testing.T) {
		mine, story, ids := storyWithScenarios(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, theirs, "", nil, "next", "--story", story) // hands out ids[0]
		mustRun(t, mine, "", nil, "scenario", "delete", ids[0])
		_, errOut, code := run(t, theirs, "", "import", shareFile(t, mine))
		if code != 67 || !strings.Contains(errOut, ids[0]) {
			t.Fatalf("exit %d, stderr %s", code, errOut)
		}
		mustRun(t, theirs, "", nil, "scenario", "show", ids[0])
		_, errOut, code = run(t, mine, "", "import", shareFile(t, theirs))
		if code != 67 || !strings.Contains(errOut, ids[0]) {
			t.Fatalf("the other way: exit %d, stderr %s", code, errOut)
		}
	})
}

func TestImportStopsOnAClash(t *testing.T) {
	t.Run("a draft edited on one side clashes, and nothing is written", func(t *testing.T) {
		mine, _, ids := storyWithScenarios(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		var before scenarioOut
		mustRun(t, theirs, "", &before, "scenario", "show", ids[1])

		mustRun(t, mine, "", nil, "scenario", "edit", ids[1], "--body", "Scenario: Choosing the format\n  Given a report\n  Then I can choose csv, xlsx or ods\n")
		mustRun(t, mine, "", nil, "close", ids[0], "--test-file", "tests/Feature/ExportTest.php", "--test", "exports a report as xlsx")
		_, errOut, code := run(t, theirs, "", "import", shareFile(t, mine))
		if code != 67 || !strings.Contains(errOut, ids[1]) || !strings.Contains(errOut, "nothing was imported") {
			t.Fatalf("exit %d, stderr %s", code, errOut)
		}
		var after, first scenarioOut
		mustRun(t, theirs, "", &after, "scenario", "show", ids[1])
		mustRun(t, theirs, "", &first, "scenario", "show", ids[0])
		if after.Lines[len(after.Lines)-1].Text != before.Lines[len(before.Lines)-1].Text || first.Status != "open" {
			t.Fatalf("import wrote something: %#v, %#v", after, first)
		}
	})

	t.Run("two people adding to the same story clash", func(t *testing.T) {
		mine, story, _ := storyWithScenarios(t)
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, mine, "Scenario: Mine\n  Then mine\n", nil, "scenario", "add", "--story", story, "--body", "-")
		mustRun(t, theirs, "Scenario: Theirs\n  Then theirs\n", nil, "scenario", "add", "--story", story, "--body", "-")
		if code := errorCode(t, theirs, "", "import", shareFile(t, mine)); code != "conflict" {
			t.Fatalf("code = %q", code)
		}
	})

	t.Run("a different answer recorded on each side clashes", func(t *testing.T) {
		mine, story, _ := storyWithScenarios(t)
		q := ask(t, mine, story, "Green or blue?")
		theirs := newProject(t)
		mustRun(t, theirs, "", nil, "import", shareFile(t, mine))
		mustRun(t, mine, "", nil, "question", "answer", q.ID, "--body", "Green.")
		mustRun(t, theirs, "", nil, "question", "answer", q.ID, "--body", "Blue.")
		if code := errorCode(t, theirs, "", "import", shareFile(t, mine)); code != "conflict" {
			t.Fatalf("code = %q", code)
		}
	})
}

func TestImportRefusesBadFiles(t *testing.T) {
	dir := newProject(t)
	write := func(text string) string {
		path := filepath.Join(t.TempDir(), "f.yaml")
		os.WriteFile(path, []byte(text), 0o644)
		return path
	}
	for name, text := range map[string]string{
		"a newer format":   "format: 99\nfeature:\n  id: \"abcdef\"\n  title: \"X\"\n  created_at: \"2026-11-06T00:00:00Z\"\n",
		"not an aat file":  "hello: world\n",
		"not YAML at all":  "{{{",
		"an unknown field": "format: 1\nfeature:\n  id: \"abcdef\"\n  title: \"X\"\n  created_at: \"2026-11-06T00:00:00Z\"\n  colour: \"red\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if code := errorCode(t, dir, "", "import", write(text)); code != "validation" {
				t.Fatalf("code = %q", code)
			}
		})
	}
	t.Run("a missing file", func(t *testing.T) {
		if code := errorCode(t, dir, "", "import", filepath.Join(t.TempDir(), "nope.yaml")); code != "not_found" {
			t.Fatalf("code = %q", code)
		}
	})
}
