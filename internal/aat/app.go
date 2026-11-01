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
	"unicode"
	"unicode/utf8"
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
  feature show <feature>                        a feature's current scenarios, and its retired ones
  story add --feature F --title T [--description D]
                                                add a story (a piece of work) to a feature
  story show <story>                            a story with its feature and scenarios
  scenario add --story S --body B               add Gherkin scenarios to a story
  scenario show <scenario>                      one scenario, with what it replaced or what replaced it
  scenario replace <scenario> --story S --reason R --body B
                                                replace a scenario with one new scenario in a story
  scenario retire <scenario> --story S --reason R
                                                retire a scenario as part of a story
  next --story S                                what to do next: a question asked mid-build, else the
                                                first open scenario or planned check-in, in order
  close <scenario> --test-file F --test NAME    close a scenario with the test that proves it
  question ask --story S --body B               raise a question; it holds the story until resolved
  question plan --story S --body B              plan a check-in after everything in the story so far;
                                                it holds the story once next reaches it
  question answer <question> --body B           record one round of the user's answer
  question show <question>                      a question with all its rounds
  question resolve <question> --note N [--scenarios G [--replaces SC] [--test-file F --test NAME]]
                                                settle it, adding any new Gherkin scenarios;
                                                the one new scenario can replace a current one,
                                                and with a test it arrives closed
  publish [--feature F] [--dir D]               write each feature as a .feature file for Gherkin
                                                tools (default dir: features/ at the project root)
  export <story> [--output FILE]                the story as a markdown review (not JSON): each
                                                scenario with its test, each question with its answers
  version                                       print the version

Text flags (D, B, N, G) take literal text, @path to read a file, or - to read stdin.`

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
	if text, ok := out.(rawText); ok {
		io.WriteString(streams.Stdout, string(text))
		return 0
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
	if (cmd == "feature" || cmd == "story" || cmd == "scenario" || cmd == "question") && len(rest) > 0 {
		cmd, rest = cmd+" "+rest[0], rest[1:]
	}
	switch cmd {
	case "feature add":
		return featureAdd(ctx, store, rest, stdin)
	case "feature list":
		features, err := store.ListFeatures(ctx)
		return map[string]any{"features": features}, err
	case "feature show":
		return featureShow(ctx, store, rest)
	case "story add":
		return storyAdd(ctx, store, rest, stdin)
	case "story show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat story show <story>")
		}
		return store.GetStory(ctx, rest[0])
	case "scenario add":
		return scenarioAdd(ctx, store, rest, stdin)
	case "scenario show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat scenario show <scenario>")
		}
		return store.GetScenario(ctx, rest[0])
	case "scenario replace":
		return scenarioReplace(ctx, store, rest, stdin)
	case "scenario retire":
		return scenarioRetire(ctx, store, rest, stdin)
	case "next":
		return next(ctx, store, rest)
	case "close":
		return closeScenario(ctx, store, rest, dir)
	case "question ask":
		return questionAdd(ctx, "question ask", rest, stdin, store.AskQuestion)
	case "question plan":
		return questionAdd(ctx, "question plan", rest, stdin, store.PlanQuestion)
	case "question answer":
		return questionAnswer(ctx, store, rest, stdin)
	case "question resolve":
		return questionResolve(ctx, store, rest, stdin, dir)
	case "question show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat question show <question>")
		}
		return store.GetQuestion(ctx, rest[0])
	case "publish":
		return publish(ctx, store, rest, dir)
	case "export":
		return export(ctx, store, rest, dir)
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
	// Check now that publish will be able to write it, because nothing can
	// change a feature once it is added.
	probe := Feature{ID: "check", Title: t, Description: d}
	if err := readsBack(renderFeature(probe, nil), probe, nil); err != nil {
		return nil, validationErr("aat feature add: this can't go in a .feature file: %v", err)
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
	sc, q, err := store.Next(ctx, *story)
	if err != nil {
		return nil, err
	}
	if q != nil {
		return map[string]any{"question": q}, nil
	}
	return map[string]any{"scenario": sc}, nil
}

// closeScenario closes a scenario with the test that proves it.
func closeScenario(ctx context.Context, store *Store, args []string, dir string) (any, error) {
	id, rest, err := leadingID(args, "usage: aat close <scenario> --test-file F --test NAME")
	if err != nil {
		return nil, err
	}
	fs := newFlags("close")
	testFile, testName := testFlags(fs)
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *testFile == "" || strings.TrimSpace(*testName) == "" {
		return nil, usageErr("aat close: --test-file and --test are both required")
	}

	sc, err := store.GetScenario(ctx, id)
	if err != nil {
		return nil, err
	}
	switch sc.Status {
	case "closed":
		return nil, conflictErr("scenario %q is already closed", id)
	case "replaced":
		return nil, conflictErr("scenario %q was replaced by %s; build that one instead", id, sc.ReplacedBy)
	case "retired":
		return nil, conflictErr("scenario %q is retired: %s", id, sc.Reason)
	}
	test, err := checkTest(dir, *testFile, *testName)
	if err != nil {
		return nil, err
	}
	others, err := store.CloseScenario(ctx, id, test)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "status": "closed", "test_file": test.File, "test": test.Name, "check": testCheck}
	var warnings []string
	if len(others) > 0 {
		warnings = sameTestWarnings(others)
	}
	if warnings = append(warnings, replacedTestWarnings(dir, sc.Replaces, test)...); len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

// testFlags adds the two flags that name the test proving a scenario.
func testFlags(fs *flag.FlagSet) (file, name *string) {
	return fs.String("test-file", "", "test file path"), fs.String("test", "", "test name")
}

// testCheck says what aat checked about a test. It never says "verified":
// running the test is the reviewer's job.
const testCheck = "found the test name in the file"

// checkTest is the deliberately light check behind closing a scenario: the
// test file must exist inside the project and contain the test name as text.
// It catches typos and invented names. The returned file path is relative
// to the project root.
func checkTest(dir, testFile, testName string) (TestRef, error) {
	name := strings.TrimSpace(testName)
	path := testFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	rel, ok := relativeTo(projectRoot(dir), path)
	if !ok {
		return TestRef{}, validationErr("test file %s is outside the project", testFile)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return TestRef{}, validationErr("can't read test file %s: %v", testFile, err)
	}
	if !containsTestName(string(content), name) {
		return TestRef{}, validationErr("test name %q not found in %s", name, testFile)
	}
	return TestRef{File: rel, Name: name}, nil
}

// containsTestName reports whether name appears in content as a whole test
// name: the character after it must not carry the name on (a letter, digit,
// underscore or space). So "test_adding_an_item" is not found inside
// "test_adding_an_item_twice", nor "requires a postcode" inside "requires a
// postcode only for UK addresses".
func containsTestName(content, name string) bool {
	for from := 0; ; {
		i := strings.Index(content[from:], name)
		if i < 0 {
			return false
		}
		end := from + i + len(name)
		r, _ := utf8.DecodeRuneInString(content[end:])
		if end == len(content) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == ' ') {
			return true
		}
		from += i + 1
	}
}

// relativeTo returns path relative to root, with forward slashes, and false
// when path is outside root.
func relativeTo(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func sameTestWarnings(others []string) []string {
	return []string{fmt.Sprintf("the same test already closes scenario %s; check it really proves both", strings.Join(others, ", "))}
}
