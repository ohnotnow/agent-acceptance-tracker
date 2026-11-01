package aat

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var Version = "dev" // overwritten by -ldflags at release

type Streams struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

const usage = `usage: aat [--db path] <command> [flags]

  init                                          set up aat in this project
  feature add --title T [--description D]       add a feature (a long-lived capability)
  feature list                                  list features
  story add --feature F --title T [--description D]
                                                add a story (a piece of work) to a feature
  story show <story>                            a story with its feature and scenarios
  scenario add --story S --body B               add Gherkin scenarios to a story
  next --story S                                the story's next open scenario
  close <scenario> --test-file F --test NAME    close a scenario with the test that proves it
  version                                       print the version

D and B take literal text, @path to read a file, or - to read stdin.`

// Run executes one aat command from dir and returns the process exit code.
func Run(ctx context.Context, args []string, dir string, streams Streams) int {
	out, err := dispatch(ctx, args, dir, streams.Stdin)
	if err != nil {
		var ce *CLIError
		if !errors.As(err, &ce) {
			ce = &CLIError{"internal", err.Error(), 1}
		}
		writeJSON(streams.Stderr, map[string]any{"error": map[string]string{"code": ce.Code, "message": ce.Message}})
		return ce.ExitCode
	}
	writeJSON(streams.Stdout, out)
	return 0
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // Gherkin text can contain <placeholders>
	enc.Encode(v)
}

func dispatch(ctx context.Context, args []string, dir string, stdin io.Reader) (any, error) {
	global := flag.NewFlagSet("aat", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dbFlag := global.String("db", "", "database path")
	if err := global.Parse(args); err != nil {
		return nil, usageErr("%v\n%s", err, usage)
	}
	args = global.Args()
	if len(args) == 0 {
		return nil, usageErr("%s", usage)
	}

	dbPath := *dbFlag
	if dbPath == "" {
		dbPath = filepath.Join(projectRoot(dir), ".aat", "aat.db")
	}

	switch args[0] {
	case "version":
		return map[string]string{"version": Version}, nil
	case "init":
		return runInit(ctx, dir, dbPath, *dbFlag != "")
	}

	if _, err := os.Stat(dbPath); err != nil {
		return nil, uninitialisedErr(dbPath)
	}
	store, err := OpenStore(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	cmd, rest := args[0], args[1:]
	if (cmd == "feature" || cmd == "story" || cmd == "scenario") && len(rest) > 0 {
		cmd, rest = cmd+" "+rest[0], rest[1:]
	}
	switch cmd {
	case "feature add":
		return featureAdd(ctx, store, rest, stdin)
	case "feature list":
		features, err := store.ListFeatures(ctx)
		return map[string]any{"features": features}, err
	case "story add":
		return storyAdd(ctx, store, rest, stdin)
	case "story show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat story show <story>")
		}
		return store.GetStory(ctx, rest[0])
	case "scenario add":
		return scenarioAdd(ctx, store, rest, stdin)
	case "next":
		return next(ctx, store, rest)
	case "close":
		return closeScenario(ctx, store, rest, dir)
	}
	return nil, usageErr("unknown command %q\n%s", strings.Join(args, " "), usage)
}

// projectRoot is the nearest directory at or above dir holding .git, or dir itself.
func projectRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

func runInit(ctx context.Context, dir, dbPath string, customDB bool) (any, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	store, err := OpenStore(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	store.Close()

	result := map[string]string{"db": dbPath}
	root := projectRoot(dir)
	if _, err := os.Stat(filepath.Join(root, ".git")); customDB || err != nil {
		result["note"] = ".gitignore not updated: no git repository, or --db was given"
		return result, nil
	}
	if err := ensureIgnored(filepath.Join(root, ".gitignore"), ".aat/"); err != nil {
		return nil, err
	}
	return result, nil
}

func ensureIgnored(path, entry string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == entry {
			return nil
		}
	}
	text := string(existing)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text+entry+"\n"), 0o644)
}

// readText resolves a text flag: "-" reads stdin, "@path" reads a file, anything else is literal.
func readText(value string, stdin io.Reader) (string, error) {
	var b []byte
	var err error
	switch {
	case value == "-":
		b, err = io.ReadAll(stdin)
	case strings.HasPrefix(value, "@"):
		b, err = os.ReadFile(value[1:])
		if err != nil {
			return "", validationErr("read %s: %v", value[1:], err)
		}
	default:
		return strings.TrimSpace(value), nil
	}
	return strings.TrimSpace(string(b)), err
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageErr("aat %s: %v", fs.Name(), err)
	}
	if fs.NArg() > 0 {
		return usageErr("aat %s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

func titleAndDescription(fs *flag.FlagSet, title, description *string, stdin io.Reader) (string, string, error) {
	t := strings.TrimSpace(*title)
	if t == "" {
		return "", "", validationErr("aat %s: --title is required", fs.Name())
	}
	d, err := readText(*description, stdin)
	return t, d, err
}

func featureAdd(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	fs := newFlags("feature add")
	title := fs.String("title", "", "feature title")
	description := fs.String("description", "", "feature description")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	t, d, err := titleAndDescription(fs, title, description, stdin)
	if err != nil {
		return nil, err
	}
	return store.AddFeature(ctx, t, d)
}

func storyAdd(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	fs := newFlags("story add")
	feature := fs.String("feature", "", "feature id")
	title := fs.String("title", "", "story title")
	description := fs.String("description", "", "story description")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *feature == "" {
		return nil, usageErr("aat story add: --feature is required")
	}
	t, d, err := titleAndDescription(fs, title, description, stdin)
	if err != nil {
		return nil, err
	}
	return store.AddStory(ctx, *feature, t, d)
}

func scenarioAdd(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	fs := newFlags("scenario add")
	story := fs.String("story", "", "story id")
	body := fs.String("body", "", "Gherkin scenarios")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *story == "" || *body == "" {
		return nil, usageErr("aat scenario add: --story and --body are required")
	}
	text, err := readText(*body, stdin)
	if err != nil {
		return nil, err
	}
	parsed, err := parseScenarios(text)
	if err != nil {
		return nil, err
	}
	added, err := store.AddScenarios(ctx, *story, parsed)
	if err != nil {
		return nil, err
	}
	refs := make([]map[string]string, len(added))
	for i, sc := range added {
		refs[i] = map[string]string{"id": sc.ID, "title": sc.Title}
	}
	return map[string]any{"scenarios": refs}, nil
}

func next(ctx context.Context, store *Store, args []string) (any, error) {
	fs := newFlags("next")
	story := fs.String("story", "", "story id")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *story == "" {
		return nil, usageErr("aat next: --story is required")
	}
	sc, err := store.NextScenario(ctx, *story)
	if err != nil {
		return nil, err
	}
	return map[string]any{"scenario": sc}, nil
}

// closeScenario closes a scenario with the test that proves it. The check
// is deliberately light: the file must exist inside the project and contain
// the test name as text. It catches typos and invented names; running the
// test is the reviewer's job.
func closeScenario(ctx context.Context, store *Store, args []string, dir string) (any, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usageErr("usage: aat close <scenario> --test-file F --test NAME")
	}
	id := args[0]
	fs := newFlags("close")
	testFile := fs.String("test-file", "", "test file path")
	testName := fs.String("test", "", "test name")
	if err := parseFlags(fs, args[1:]); err != nil {
		return nil, err
	}
	if *testFile == "" || strings.TrimSpace(*testName) == "" {
		return nil, usageErr("aat close: --test-file and --test are both required")
	}
	name := strings.TrimSpace(*testName)

	sc, err := store.GetScenario(ctx, id)
	if err != nil {
		return nil, err
	}
	if sc.Status == "closed" {
		return nil, conflictErr("scenario %q is already closed", id)
	}

	root := projectRoot(dir)
	path := *testFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, validationErr("test file %s is outside the project", *testFile)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, validationErr("can't read test file %s: %v", *testFile, err)
	}
	if !strings.Contains(string(content), name) {
		return nil, validationErr("test name %q not found in %s", name, *testFile)
	}

	rel = filepath.ToSlash(rel)
	others, err := store.CloseScenario(ctx, id, rel, name)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":        id,
		"status":    "closed",
		"test_file": rel,
		"test":      name,
		"check":     "found the test name in the file",
	}
	if len(others) > 0 {
		out["warnings"] = []string{fmt.Sprintf("the same test already closes scenario %s; check it really proves both", strings.Join(others, ", "))}
	}
	return out, nil
}
