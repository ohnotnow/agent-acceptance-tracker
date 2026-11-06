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
  feature list                                  list features, with how many stories each has in each
                                                state
  feature show <feature>                        a feature's current scenarios, and its retired ones
  stories [--state STATE | --open]              every story, newest-touched first, each in one state:
                                                draft, building, built or accepted; --open leaves out
                                                accepted ones
  story add --feature F --title T [--description D]
                                                add a story (a piece of work) to a feature
  story show <story>                            a story's scenarios and planned check-ins in order,
                                                its open questions and its retirements
  story edit <story> [--title T] [--description D]
                                                change a story's title or description until it is
                                                accepted
  story agree <story> --note N                  record that the user signed the story off for
                                                building; until then it is a draft, and next
                                                refuses it
  story accept <story> --note N                 record that the user reviewed the finished story
                                                and accepted it; it then takes no more work
  scenario add --story S --body B [--before STEP]
                                                add Gherkin scenarios to a story, at the end or in
                                                front of a step not yet built
  scenario edit <scenario> --body B             rewrite a scenario not yet closed, in place
  scenario delete <scenario>                    delete a scenario never built (not one that replaces
                                                a rule or came out of a question)
  scenario move <scenario> --before STEP        move a scenario not yet closed in front of another
                                                step not yet built or settled
  scenario show <scenario>                      one scenario, with what it replaced or what replaced it
  scenario replace <scenario> --story S --reason R --body B
                                                replace a scenario with one new scenario in a story
  scenario retire <scenario> --story S --reason R
                                                retire a scenario as part of a story
  next --story S                                what to do next: a question asked mid-build, else a
                                                retirement to remove, else the first open scenario
                                                or planned check-in, in order
  close <scenario> --test-file F --test NAME    close a scenario with the test that proves it
  close <scenario>                              close a retired scenario once its behaviour and
                                                test are removed (checks the test name is gone)
  question ask --story S --body B               raise a question; it holds the story until resolved
  question plan --story S --body B [--before STEP]
                                                plan a check-in after everything in the story so far,
                                                or in front of a step not yet built; it holds the
                                                story once next reaches it
  question edit <question> --body B             rewrite an open question nobody has answered yet
  question move <question> --before STEP        move an open check-in in front of another step not
                                                yet built or settled
  question answer <question> --body B           record one round of the user's answer
  question show <question>                      a question with all its rounds and any corrections
  question correct <question> (--round N | --resolve-note) --body T --reason R
                                                correct a recorded answer or resolve note; the
                                                old text and the reason stay on the record
  question resolve <question> --note N [--scenarios G [--replaces SC] [--test-file F --test NAME]]
                                                settle it, adding any new Gherkin scenarios;
                                                the one new scenario can replace a current one,
                                                and with a test it arrives closed
  publish [--feature F] [--dir D]               write each feature as a .feature file for Gherkin
                                                tools (default dir: features/ at the project root)
  publish --yaml [--feature F] [--dir D]        write everything aat holds about each feature as YAML,
                                                for another developer to import (default dir:
                                                features/yaml/)
  import <file>...                              merge YAML files from publish --yaml into this
                                                database; stops, writing nothing, on a clash
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
		if ce.Code == "usage" {
			ce.Message += " (aat --help lists every command)"
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
	if err := global.Parse(args); errors.Is(err, flag.ErrHelp) {
		return help(nil)
	} else if err != nil {
		return nil, usageErr("%v", err)
	}
	args = global.Args()
	if words, ok := helpWanted(args); ok {
		return help(words)
	}
	if len(usageEntries(args[:1])) == 0 {
		return nil, usageErr("unknown command %q", args[0])
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
		return featureList(ctx, store)
	case "stories":
		return listStories(ctx, store, rest)
	case "feature show":
		return featureShow(ctx, store, rest)
	case "story add":
		return storyAdd(ctx, store, rest, stdin)
	case "story show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat story show <story>")
		}
		return store.GetStory(ctx, rest[0])
	case "story agree":
		return storyAgree(ctx, store, rest, stdin)
	case "story accept":
		return storyAccept(ctx, store, rest, stdin)
	case "story edit":
		return storyEdit(ctx, store, rest, stdin)
	case "scenario add":
		return scenarioAdd(ctx, store, rest, stdin)
	case "scenario show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat scenario show <scenario>")
		}
		return store.GetScenario(ctx, rest[0])
	case "scenario edit":
		return scenarioEdit(ctx, store, rest, stdin)
	case "scenario delete":
		return scenarioDelete(ctx, store, rest)
	case "scenario move":
		return stepMove(ctx, "scenario move", rest, func(ctx context.Context, id, before string) (any, error) {
			return store.MoveScenario(ctx, id, before)
		})
	case "scenario replace":
		return scenarioReplace(ctx, store, rest, stdin)
	case "scenario retire":
		return scenarioRetire(ctx, store, rest, stdin)
	case "next":
		return next(ctx, store, rest)
	case "close":
		return closeScenario(ctx, store, rest, dir)
	case "question ask":
		return questionAdd(ctx, store, "question ask", rest, stdin, false)
	case "question plan":
		return questionAdd(ctx, store, "question plan", rest, stdin, true)
	case "question edit":
		return questionEdit(ctx, store, rest, stdin)
	case "question move":
		return stepMove(ctx, "question move", rest, func(ctx context.Context, id, before string) (any, error) {
			return store.MoveCheckIn(ctx, id, before)
		})
	case "question answer":
		return questionAnswer(ctx, store, rest, stdin)
	case "question resolve":
		return questionResolve(ctx, store, rest, stdin, dir)
	case "question correct":
		return questionCorrect(ctx, store, rest, stdin)
	case "question show":
		if len(rest) != 1 {
			return nil, usageErr("usage: aat question show <question>")
		}
		return store.GetQuestion(ctx, rest[0])
	case "publish":
		return publish(ctx, store, rest, dir)
	case "export":
		return export(ctx, store, rest, dir)
	case "import":
		return importFiles(ctx, store, rest, dir)
	}
	return nil, usageErr("unknown command %q", strings.Join(args, " "))
}

// helpWanted reports whether args ask for help, and about which command:
// no command at all, "help [command...]", or -h/--help anywhere after the
// command's words.
func helpWanted(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, true
	}
	if args[0] == "help" {
		return args[1:], true
	}
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			return commandWords(args), true
		}
	}
	return nil, false
}

// commandWords is the command's words: the args before the first flag.
func commandWords(args []string) []string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			return args[:i]
		}
	}
	return args
}

// help is the usage text for a person at a terminal: all of it, or only the
// entries for the command named by words ("story" gives every story
// command, "story agree" just that one).
// Words after the command's own ("story show XZaWlH --help") are dropped
// until something matches.
func help(words []string) (any, error) {
	if len(words) == 0 {
		return rawText(usage + "\n"), nil
	}
	for n := len(words); n > 0; n-- {
		if picked := usageEntries(words[:n]); len(picked) > 0 {
			lines := strings.Split(usage, "\n")
			return rawText(lines[0] + "\n\n" + strings.Join(picked, "\n") + "\n\n" + lines[len(lines)-1] + "\n"), nil
		}
	}
	return nil, usageErr("unknown command %q", strings.Join(words, " "))
}

// usageEntries is the lines of every usage entry whose command starts with words.
func usageEntries(words []string) []string {
	var picked []string
	keep := false
	for _, l := range strings.Split(usage, "\n") {
		switch {
		case strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   "): // an entry's first line
			fields := strings.Fields(l)
			keep = len(fields) >= len(words) && strings.Join(fields[:len(words)], " ") == strings.Join(words, " ")
		case !strings.HasPrefix(l, "   "): // a line that isn't part of an entry
			keep = false
		}
		if keep {
			picked = append(picked, l)
		}
	}
	return picked
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

func storyAccept(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	id, rest, err := leadingID(args, "usage: aat story accept <story> --note TEXT")
	if err != nil {
		return nil, err
	}
	fs := newFlags("story accept")
	noteFlag := fs.String("note", "", "what the user said on accepting it")
	if err := parseFlags(fs, rest); err != nil {
		return nil, err
	}
	if *noteFlag == "" {
		return nil, usageErr("aat story accept: --note is required")
	}
	note, err := readText(*noteFlag, stdin)
	if err != nil {
		return nil, err
	}
	if note == "" {
		return nil, validationErr("aat story accept: the note is empty")
	}
	st, err := store.AcceptStory(ctx, id, note)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": st.ID, "accepted_at": st.AcceptedAt, "accept_note": st.AcceptNote}, nil
}

func scenarioAdd(ctx context.Context, store *Store, args []string, stdin io.Reader) (any, error) {
	fs := newFlags("scenario add")
	story := fs.String("story", "", "story id")
	body := fs.String("body", "", "Gherkin scenarios")
	before := fs.String("before", "", "the step to put the scenarios in front of")
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
	added, err := store.AddScenarios(ctx, *story, parsed, *before)
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
	if err := store.requireAgreed(ctx, *story); err != nil {
		return nil, err
	}
	sc, q, err := store.Next(ctx, *story)
	if err != nil {
		return nil, err
	}
	if q != nil {
		return map[string]any{"question": q}, nil
	}
	if sc != nil && sc.Status == "retired" {
		return map[string]any{"retirement": sc}, nil
	}
	if sc != nil {
		if err := store.MarkHandedOut(ctx, sc); err != nil {
			return nil, err
		}
	}
	return map[string]any{"scenario": sc}, nil
}

// closeScenario closes a scenario with the test that proves it, or a retired
// scenario once its test is gone.
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
	withTest := *testFile != "" || *testName != ""

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
		if withTest {
			return nil, conflictErr("scenario %q is retired (%s); once its behaviour and test are removed, close it with no test: aat close %s", id, sc.Reason, id)
		}
		return closeRetirement(ctx, store, sc, dir)
	}
	if *testFile == "" || strings.TrimSpace(*testName) == "" {
		return nil, usageErr("aat close: --test-file and --test are both required")
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

// closeRetirement records that a retired scenario's behaviour and test were
// removed. The check is the close check reversed: the test name must no
// longer be in its file. A retired scenario that was never built is checked
// against the test of the rule it replaced, if any, which is the test still
// standing for this behaviour.
func closeRetirement(ctx context.Context, store *Store, sc Scenario, dir string) (any, error) {
	if sc.RemovedAt != "" {
		return nil, conflictErr("scenario %q is retired and its removal is already closed", sc.ID)
	}
	test := TestRef{File: sc.TestFile, Name: sc.Test}
	if test.Name == "" && sc.Replaces != nil {
		test = TestRef{File: sc.Replaces.TestFile, Name: sc.Replaces.Test}
	}
	check := "no test was recorded for this behaviour, so there was nothing to look for"
	if test.Name != "" {
		if testStillThere(dir, test) {
			return nil, validationErr("test %q is still in %s; remove it with the retired behaviour, then close this again", test.Name, test.File)
		}
		check = fmt.Sprintf("test %q is no longer in %s", test.Name, test.File)
	}
	removedAt, err := store.RemoveRetired(ctx, sc.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": sc.ID, "status": sc.Status, "removed_at": removedAt, "check": check}, nil
}

// testStillThere reports whether a test's name is still in its file, which is
// relative to the project root. A missing file means it isn't.
func testStillThere(dir string, test TestRef) bool {
	content, err := os.ReadFile(filepath.Join(projectRoot(dir), filepath.FromSlash(test.File)))
	return err == nil && containsTestName(string(content), test.Name)
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
