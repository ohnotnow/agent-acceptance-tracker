package aat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

type publishOut struct {
	Files []struct {
		Path      string `json:"path"`
		Feature   string `json:"feature"`
		Scenarios int    `json:"scenarios"`
	} `json:"files"`
	Check string `json:"check"`
}

// featureWithTwoStories is storyWithScenarios plus a second story on the same
// feature holding one free-form scenario. It returns the project dir, the
// feature id and the three scenario ids in the order they should publish.
func featureWithTwoStories(t *testing.T) (string, string, []string) {
	t.Helper()
	dir, story, ids := storyWithScenarios(t)
	var shown storyOut
	mustRun(t, dir, "", &shown, "story", "show", story)
	second := addStory(t, dir, shown.Feature.ID, "Make exports quick")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, "Scenario: Feels quick\n  Exporting should feel instant on an ordinary report.\n", &added,
		"scenario", "add", "--story", second.ID, "--body", "-")
	return dir, shown.Feature.ID, append(ids, added.Scenarios[0].ID)
}

func TestPublishingFeatureFiles(t *testing.T) {
	t.Run("each feature becomes one .feature file, with its stories' scenarios in order", func(t *testing.T) {
		dir, feature, ids := featureWithTwoStories(t)
		var out publishOut
		mustRun(t, dir, "", &out, "publish")
		if len(out.Files) != 1 || out.Files[0].Path != "features/exporting_reports.feature" || out.Files[0].Scenarios != 3 {
			t.Fatalf("out = %#v", out)
		}
		if out.Check != "read back with the official Gherkin parser" {
			t.Fatalf("check = %q", out.Check)
		}
		got, err := os.ReadFile(filepath.Join(dir, "features", "exporting_reports.feature"))
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`# Written by aat publish from feature %s. Edit the spec in aat, not this file.
Feature: Exporting reports
  about Exporting reports

  @aat-%s
  Scenario: Exporting as xlsx
    Given a report with some rows
    When I export it as xlsx
    Then I get an xlsx file
    And it has the same rows as the csv export

  @aat-%s
  Scenario: Choosing the format
    Given a report
    When I open the export menu
    Then I can choose csv or xlsx

  @aat-%s
  Scenario: Feels quick
    Exporting should feel instant on an ordinary report.
`, feature, ids[0], ids[1], ids[2])
		if string(got) != want {
			t.Fatalf("file =\n%s\nwant =\n%s", got, want)
		}
	})

	t.Run("the file reads back with Cucumber's own parser", func(t *testing.T) {
		dir, _, _ := featureWithTwoStories(t)
		mustRun(t, dir, "", nil, "publish")
		f, _ := os.Open(filepath.Join(dir, "features", "exporting_reports.feature"))
		defer f.Close()
		doc, err := gherkin.ParseGherkinDocument(f, (&messages.Incrementing{}).NewId)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range doc.Feature.Children {
			names = append(names, c.Scenario.Name)
		}
		if got := strings.Join(names, " | "); got != "Exporting as xlsx | Choosing the format | Feels quick" {
			t.Fatalf("scenarios = %s", got)
		}
	})

	t.Run("--feature publishes just that one, and --dir puts it somewhere else", func(t *testing.T) {
		dir, feature, _ := featureWithTwoStories(t)
		addFeature(t, dir, "Signing up")
		var out publishOut
		mustRun(t, dir, "", &out, "publish", "--feature", feature, "--dir", "spec")
		if len(out.Files) != 1 || out.Files[0].Path != "spec/exporting_reports.feature" {
			t.Fatalf("out = %#v", out)
		}
		if _, err := os.Stat(filepath.Join(dir, "spec", "signing_up.feature")); !os.IsNotExist(err) {
			t.Fatalf("the other feature should not be published: %v", err)
		}
	})

	t.Run("publishing again replaces the file aat wrote", func(t *testing.T) {
		dir, _, _ := featureWithTwoStories(t)
		mustRun(t, dir, "", nil, "publish")
		var shown struct {
			Features []featureOut `json:"features"`
		}
		mustRun(t, dir, "", &shown, "feature", "list")
		later := addStory(t, dir, shown.Features[0].ID, "Large reports")
		mustRun(t, dir, "Scenario: A million rows\n  Given a report with a million rows\n  Then the export still works\n", nil,
			"scenario", "add", "--story", later.ID, "--body", "-")
		mustRun(t, dir, "", nil, "publish")
		got, _ := os.ReadFile(filepath.Join(dir, "features", "exporting_reports.feature"))
		if !strings.Contains(string(got), "Scenario: A million rows") {
			t.Fatalf("the republished file should have the new scenario:\n%s", got)
		}
	})

	t.Run("a file aat published for a different feature is never overwritten", func(t *testing.T) {
		dir, _, _ := featureWithTwoStories(t)
		os.MkdirAll(filepath.Join(dir, "features"), 0o755)
		someoneElses := publishedBy("Zz99Zz") + "\nFeature: Exporting reports\n"
		os.WriteFile(filepath.Join(dir, "features", "exporting_reports.feature"), []byte(someoneElses), 0o644)
		if code := errorCode(t, dir, "", "publish"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
	})

	t.Run("it never writes through a symlink, even a dangling one", func(t *testing.T) {
		dir, _, _ := featureWithTwoStories(t)
		os.MkdirAll(filepath.Join(dir, "features"), 0o755)
		target := filepath.Join(t.TempDir(), "elsewhere.feature")
		os.Symlink(target, filepath.Join(dir, "features", "exporting_reports.feature"))
		if code := errorCode(t, dir, "", "publish"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("the symlink's target should not be created: %v", err)
		}
	})

	t.Run("features whose titles make the same file name each get their own file", func(t *testing.T) {
		dir := newProject(t)
		first := addFeature(t, dir, "Sign-up")
		second := addFeature(t, dir, "Sign up")
		var out publishOut
		mustRun(t, dir, "", &out, "publish")
		if len(out.Files) != 2 || out.Files[0].Path != "features/sign_up.feature" || out.Files[1].Path != "features/sign_up_"+second.ID+".feature" {
			t.Fatalf("files = %#v (first %s, second %s)", out.Files, first.ID, second.ID)
		}
		mustRun(t, dir, "", &out, "publish", "--feature", second.ID)
		if len(out.Files) != 1 || out.Files[0].Path != "features/sign_up_"+second.ID+".feature" {
			t.Fatalf("publishing the second alone should keep its name: %#v", out.Files)
		}
	})

	t.Run("a file aat didn't write is never overwritten, and nothing is written", func(t *testing.T) {
		dir, _, _ := featureWithTwoStories(t)
		addFeature(t, dir, "Signing up")
		os.MkdirAll(filepath.Join(dir, "features"), 0o755)
		handWritten := "Feature: Exporting reports\n  Written by a person.\n"
		os.WriteFile(filepath.Join(dir, "features", "exporting_reports.feature"), []byte(handWritten), 0o644)
		if code := errorCode(t, dir, "", "publish"); code != "conflict" {
			t.Fatalf("code = %q, want conflict", code)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "features", "exporting_reports.feature"))
		if string(got) != handWritten {
			t.Fatalf("the hand-written file was changed:\n%s", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "features", "signing_up.feature")); !os.IsNotExist(err) {
			t.Fatalf("no file should be written when one is refused: %v", err)
		}
	})

	// Nothing can change a feature once added, so a description that
	// couldn't be published is refused when the feature is added.
	for name, description := range map[string]string{
		"a markdown heading": "# Exporting reports\n\nEverything about exports.",
		"a Scenario line":    "What it covers:\nScenario: this line would start a scenario",
		"a tag-like line":    "@everyone can export",
	} {
		t.Run("a feature description with "+name+" is refused when the feature is added", func(t *testing.T) {
			dir := newProject(t)
			if code := errorCode(t, dir, "", "feature", "add", "--title", "Exporting reports", "--description", description); code != "validation" {
				t.Fatalf("code = %q, want validation", code)
			}
			var list struct {
				Features []featureOut `json:"features"`
			}
			mustRun(t, dir, "", &list, "feature", "list")
			if len(list.Features) != 0 {
				t.Fatalf("nothing should be stored: %#v", list.Features)
			}
		})
	}

	t.Run("an unknown feature is refused", func(t *testing.T) {
		dir := newProject(t)
		if code := errorCode(t, dir, "", "publish", "--feature", "nope00"); code != "not_found" {
			t.Fatalf("code = %q, want not_found", code)
		}
	})
}

func TestFeatureFileNames(t *testing.T) {
	cases := map[string]string{
		"Exporting reports":         "exporting_reports.feature",
		"  Sign-up & log-in (v2)  ": "sign_up_log_in_v2.feature",
		"Café orders":               "café_orders.feature",
		"***":                       "feature_Ab12Cd.feature",
	}
	for title, want := range cases {
		if got := featureFileName(title, "Ab12Cd"); got != want {
			t.Errorf("featureFileName(%q) = %q, want %q", title, got, want)
		}
	}
}
