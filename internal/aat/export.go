package aat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rawText is command output written as it is, not as JSON.
type rawText string

// export renders a story as a markdown review: what was agreed, in the order
// the builder met it, each scenario with the test that claims to prove it,
// and every question with the user's answers. It is a reading view for a
// person, such as a colleague asked for a second pair of eyes; nothing reads
// it back, so its layout can change freely.
func export(ctx context.Context, store *Store, args []string, dir string) (any, error) {
	id, rest, err := leadingID(args, "usage: aat export <story> [--output FILE]")
	if err != nil {
		return nil, err
	}
	fs := newFlags("export")
	output := fs.String("output", "", "write to this file instead of stdout")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	st, err := store.GetStory(ctx, id)
	if err != nil {
		return nil, err
	}
	qs, err := store.StoryQuestions(ctx, id)
	if err != nil {
		return nil, err
	}
	text := renderStory(st, qs)
	if *output == "" {
		return rawText(text), nil
	}
	path := *output
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	root := projectRoot(dir)
	marker := frontMatter(st)
	if err := mayReplace(path, root, "story "+st.ID, func(content string) bool {
		return strings.HasPrefix(content, marker)
	}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return map[string]string{"path": displayPath(root, path)}, nil
}

// frontMatter opens an exported story; --output only replaces a file that
// starts with the same lines.
func frontMatter(st Story) string {
	return fmt.Sprintf("---\nid: %s\nkind: story\n", st.ID)
}

func renderStory(st Story, qs []Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sfeature: %s\ncreated_at: %s\n---\n\n", frontMatter(st), st.Feature.ID, st.CreatedAt)
	fmt.Fprintf(&b, "## %s\n\nFeature: %s\n\n", st.Title, st.Feature.Title)
	if st.Description != "" {
		b.WriteString(st.Description + "\n\n")
	}

	// Scenarios and planned check-ins, in the story's order. qs has every
	// question with its rounds; the steps have the planned ones without.
	withRounds := map[string]Question{}
	var asked []Question
	for _, q := range qs {
		withRounds[q.ID] = q
		if q.Position == 0 {
			asked = append(asked, q)
		}
	}
	for n, step := range st.Steps {
		if step.Scenario != nil {
			renderScenario(&b, fmt.Sprintf("### %d. %s", n+1, step.Scenario.Title), *step.Scenario)
		} else {
			fmt.Fprintf(&b, "### %d. Check-in %s\n\n", n+1, step.Question.ID)
			renderQuestion(&b, withRounds[step.Question.ID])
		}
	}

	if len(asked) > 0 {
		b.WriteString("## Questions raised while building\n\n")
		for _, q := range asked {
			fmt.Fprintf(&b, "### Question %s\n\n", q.ID)
			renderQuestion(&b, q)
		}
	}
	// Scenarios this story retired from other stories. Its own retired
	// scenarios are already in the list above.
	var retires []Scenario
	for _, sc := range st.Retires {
		if sc.StoryID != st.ID {
			retires = append(retires, sc)
		}
	}
	if len(retires) > 0 {
		b.WriteString("## Scenarios this story retires\n\n")
		for _, sc := range retires {
			renderScenario(&b, "### "+sc.Title, sc)
		}
	}
	return b.String()
}

func renderScenario(b *strings.Builder, heading string, sc Scenario) {
	var g strings.Builder
	fmt.Fprintf(&g, "Scenario: %s\n", sc.Title)
	writeDescription(&g, sc.Description, "  ")
	for _, l := range sc.Lines {
		fmt.Fprintf(&g, "  %s %s\n", l.Keyword, l.Text)
	}
	// A longer fence than any run of backticks in the text keeps the block whole.
	fence := "```"
	for strings.Contains(g.String(), fence) {
		fence += "`"
	}
	fmt.Fprintf(b, "%s\n\n%sgherkin\n%s%s\n\n", heading, fence, g.String(), fence)
	if r := sc.Replaces; r != nil {
		fmt.Fprintf(b, "This replaces %q (%s). Reason: %s\n\n", r.Title, r.ID, r.Reason)
		if r.Test != "" {
			fmt.Fprintf(b, "The old rule's test was `%s` in `%s`; it should now be changed or removed.\n\n", r.Test, r.TestFile)
		}
	}
	if sc.QuestionID != "" {
		fmt.Fprintf(b, "This came out of question %s.\n\n", sc.QuestionID)
	}
	switch sc.Status {
	case "closed":
		fmt.Fprintf(b, "Closed by the test `%s` in `%s`. aat found the test name in the file; it did not run the test.\n\n", sc.Test, sc.TestFile)
	case "open":
		b.WriteString("Open: not built yet.\n\n")
	case "replaced":
		fmt.Fprintf(b, "Replaced by scenario %s. Reason: %s\n\n", sc.ReplacedBy, sc.Reason)
	case "retired":
		fmt.Fprintf(b, "Retired by story %s. Reason: %s\n\n", sc.RetiredBy, sc.Reason)
		if sc.Test != "" {
			fmt.Fprintf(b, "Its test was `%s` in `%s`; it should be removed with the behaviour.\n\n", sc.Test, sc.TestFile)
		}
		if sc.RemovedAt == "" {
			b.WriteString("Removal not closed yet.\n\n")
		} else {
			fmt.Fprintf(b, "Removed: aat found the test name gone from the file (or no test to look for) on %s.\n\n", sc.RemovedAt)
		}
	}
}

func renderQuestion(b *strings.Builder, q Question) {
	for _, l := range strings.Split(q.Text, "\n") {
		b.WriteString(strings.TrimRight("> "+l, " ") + "\n")
	}
	b.WriteString("\n")
	if len(q.Rounds) > 0 {
		b.WriteString("The user's answers:\n\n")
		for i, r := range q.Rounds {
			fmt.Fprintf(b, "%d. %s\n", i+1, strings.ReplaceAll(r.Answer, "\n", "\n   "))
		}
		b.WriteString("\n")
	}
	if q.Status == "resolved" {
		fmt.Fprintf(b, "Settled: %s\n\n", q.Note)
	} else {
		b.WriteString("Still open.\n\n")
	}
}
