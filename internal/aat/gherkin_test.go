package aat

import (
	"strings"
	"testing"
)

func TestParseScenarios(t *testing.T) {
	t.Run("keywords and text are split, and the description is kept", func(t *testing.T) {
		got, err := parseScenarios("Scenario: Exporting\n  Only for admins.\n\n  Given a report\n  When I export it\n  Then I get a file\n  But not an empty one\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Title != "Exporting" || got[0].Description != "Only for admins." {
			t.Fatalf("got %#v", got)
		}
		want := []Line{{"Given", "a report"}, {"When", "I export it"}, {"Then", "I get a file"}, {"But", "not an empty one"}}
		for i, l := range want {
			if got[0].Lines[i] != l {
				t.Fatalf("line %d = %#v, want %#v", i, got[0].Lines[i], l)
			}
		}
	})

	t.Run("Example is accepted as Scenario", func(t *testing.T) {
		got, err := parseScenarios("Example: Exporting\n  Given a report\n")
		if err != nil || len(got) != 1 {
			t.Fatalf("got %#v, err %v", got, err)
		}
	})

	t.Run("a free-form scenario with only a description is accepted", func(t *testing.T) {
		got, err := parseScenarios("Scenario: Feels quick\n  Exporting should feel instant on a normal report.\n")
		if err != nil || len(got) != 1 || len(got[0].Lines) != 0 {
			t.Fatalf("got %#v, err %v", got, err)
		}
	})

	rejects := map[string]string{
		"empty input":             "   \n",
		"a Feature line":          "Feature: Exports\n  Scenario: x\n    Given y\n",
		"text before a scenario":  "Some notes\nScenario: x\n  Given y\n",
		"Background":              "Background:\n  Given a user\nScenario: x\n  Given y\n",
		"Rule":                    "Rule: one\n  Scenario: x\n    Given y\n",
		"Scenario Outline":        "Scenario Outline: x\n  Given <a>\n  Examples:\n    | a |\n    | 1 |\n",
		"tags":                    "@slow\nScenario: x\n  Given y\n",
		"a data table":            "Scenario: x\n  Given rows\n    | a |\n    | 1 |\n",
		"a doc string":            "Scenario: x\n  Given text\n    \"\"\"\n    hello\n    \"\"\"\n",
		"the star keyword":        "Scenario: x\n  * y\n",
		"an empty scenario":       "Scenario: x\n",
		"a scenario with no name": "Scenario:\n  Given y\n",
	}
	for name, body := range rejects {
		t.Run("rejects "+name, func(t *testing.T) {
			got, err := parseScenarios(body)
			if err == nil {
				t.Fatalf("expected an error, got %#v", got)
			}
		})
	}

	t.Run("parse errors report the user's own line numbers", func(t *testing.T) {
		// The doc string opened on line 3 is never closed, so the parser hits end of file on line 5.
		_, err := parseScenarios("Scenario: x\n  Given y\n    \"\"\"\n    never closed\n")
		if err == nil || !strings.Contains(err.Error(), "(5:") {
			t.Fatalf("err = %v, want it to mention line 5", err)
		}
	})
}
