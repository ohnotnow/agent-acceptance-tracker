package aat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// publishCheck says how each published file was checked before it was written.
const publishCheck = "read back with the official Gherkin parser"

// publishedBy is the first line of every file aat publishes. aat only ever
// replaces a file that starts with this line for the same feature, so a file
// written by a person is never overwritten.
func publishedBy(featureID string) string {
	return fmt.Sprintf("# Written by aat publish from feature %s. Edit the spec in aat, not this file.", featureID)
}

// publish writes each feature as a .feature file for Gherkin tools. Every
// file is rendered, read back with Cucumber's parser and checked against
// what is already on disk before any is written; if any check fails, nothing
// is written.
func publish(ctx context.Context, store *Store, args []string, dir string) (any, error) {
	fs := newFlags("publish")
	featureFlag := fs.String("feature", "", "publish only this feature")
	dirFlag := fs.String("dir", "", "where to write the files")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	root := projectRoot(dir)
	outDir := filepath.Join(root, "features")
	if *dirFlag != "" {
		outDir = *dirFlag
		if !filepath.IsAbs(outDir) {
			outDir = filepath.Join(dir, outDir)
		}
	}

	all, err := store.ListFeatures(ctx)
	if err != nil {
		return nil, err
	}
	names := featureFileNames(all)
	ids := []string{*featureFlag}
	if *featureFlag == "" {
		ids = ids[:0]
		for _, f := range all {
			ids = append(ids, f.ID)
		}
	}

	type file struct {
		path, text, featureID string
		scenarios             int
	}
	var files []file
	for _, id := range ids {
		f, err := store.GetFeature(ctx, id)
		if err != nil {
			return nil, err
		}
		scs, err := store.CurrentScenarios(ctx, id)
		if err != nil {
			return nil, err
		}
		text := renderFeature(f, scs)
		if err := readsBack(text, f, scs); err != nil {
			return nil, validationErr("feature %s (%q) can't be published: %v", f.ID, f.Title, err)
		}
		path := filepath.Join(outDir, names[id])
		marker := publishedBy(f.ID)
		if err := mayReplace(path, root, "feature "+f.ID, func(content string) bool {
			first, _, _ := strings.Cut(content, "\n")
			return first == marker
		}); err != nil {
			return nil, err
		}
		files = append(files, file{path, text, f.ID, len(scs)})
	}

	written := []map[string]any{}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(f.path, []byte(f.text), 0o644); err != nil {
			return nil, err
		}
		written = append(written, map[string]any{"path": displayPath(root, f.path), "feature": f.featureID, "scenarios": f.scenarios})
	}
	return map[string]any{"files": written, "check": publishCheck}, nil
}

func renderFeature(f Feature, scs []Scenario) string {
	var b strings.Builder
	b.WriteString(publishedBy(f.ID) + "\n")
	b.WriteString("Feature: " + f.Title + "\n")
	writeDescription(&b, f.Description, "  ")
	for _, sc := range scs {
		fmt.Fprintf(&b, "\n  @aat-%s\n  Scenario: %s\n", sc.ID, sc.Title)
		writeDescription(&b, sc.Description, "    ")
		if sc.Description != "" && len(sc.Lines) > 0 {
			b.WriteString("\n")
		}
		for _, l := range sc.Lines {
			fmt.Fprintf(&b, "    %s %s\n", l.Keyword, l.Text)
		}
	}
	return b.String()
}

func writeDescription(b *strings.Builder, text, indent string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			b.WriteString(indent + l)
		}
		b.WriteString("\n")
	}
}

// readsBack parses text with Cucumber's own parser and checks that it holds
// exactly the feature and scenarios it was rendered from. It catches text
// that Gherkin reads differently, such as a description line starting with
// "Scenario:" or "#".
func readsBack(text string, f Feature, scs []Scenario) error {
	doc, err := gherkin.ParseGherkinDocument(strings.NewReader(text), (&messages.Incrementing{}).NewId)
	if err != nil {
		return err
	}
	if doc.Feature == nil || strings.TrimSpace(doc.Feature.Name) != strings.TrimSpace(f.Title) {
		return fmt.Errorf("its title doesn't read back as written")
	}
	if dedent(doc.Feature.Description) != dedent(f.Description) {
		return fmt.Errorf("its description doesn't read back as written; check for lines starting with a Gherkin keyword, # or @")
	}
	if len(doc.Feature.Children) != len(scs) {
		return fmt.Errorf("%d scenarios read back instead of %d; check the description for lines starting with a Gherkin keyword", len(doc.Feature.Children), len(scs))
	}
	for i, c := range doc.Feature.Children {
		got, want := c.Scenario, scs[i]
		if got == nil || strings.TrimSpace(got.Name) != want.Title || dedent(got.Description) != dedent(want.Description) ||
			len(got.Tags) != 1 || got.Tags[0].Name != "@aat-"+want.ID || len(got.Steps) != len(want.Lines) {
			return fmt.Errorf("scenario %q doesn't read back as written", want.Title)
		}
		for j, st := range got.Steps {
			if strings.TrimSpace(st.Keyword) != want.Lines[j].Keyword || strings.TrimSpace(st.Text) != want.Lines[j].Text {
				return fmt.Errorf("scenario %q, line %d doesn't read back as written", want.Title, j+1)
			}
		}
	}
	return nil
}

// mayReplace refuses to write over a file unless owned says aat wrote it,
// for what ("feature X", "story X"), and never writes through a symlink.
func mayReplace(path, root, what string, owned func(content string) bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return conflictErr("%s is a symlink; aat won't write through it", displayPath(root, path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !owned(strings.ReplaceAll(string(b), "\r\n", "\n")) {
		return conflictErr("%s already exists and wasn't written by aat for %s; move it, or write somewhere else", displayPath(root, path), what)
	}
	return nil
}

// featureFileNames gives every feature its file name. Features are taken in
// the order they were added, and a later feature whose title makes the same
// name as an earlier one gets its id added, so a name never changes once
// given.
func featureFileNames(features []Feature) map[string]string {
	names, taken := map[string]string{}, map[string]bool{}
	for _, f := range features {
		name := featureFileName(f.Title, f.ID)
		if taken[name] {
			name = strings.TrimSuffix(name, ".feature") + "_" + f.ID + ".feature"
		}
		taken[name] = true
		names[f.ID] = name
	}
	return names
}

// featureFileName makes a file name from a feature's title, in the
// snake_case that Cucumber and Behave projects use.
func featureFileName(title, id string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(title) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(r)
		gap = false
	}
	if b.Len() == 0 {
		return "feature_" + id + ".feature"
	}
	return b.String() + ".feature"
}

// displayPath shows path relative to the project root when it is inside it.
func displayPath(root, path string) string {
	if rel, ok := relativeTo(root, path); ok {
		return rel
	}
	return path
}
