---
name: aat
description: Build a piece of work from an agreed Gherkin spec, one scenario at a time, test first, with aat (agent acceptance tracker). Use when a briefing or the user gives you an aat story to build, when you run `aat next`, close a scenario with its test, or raise, answer or resolve a question for the user while building. You must use this skill if a briefing asks you to build an aat story. For agreeing a story with the user, checking it before a build, coordinating a builder or reviewing a finished story, use the aat-writing skill instead.
---

# aat: build agreed behaviour one scenario at a time

`aat` holds what the app is meant to do, written as Gherkin scenarios
(Scenario, Given/When/Then), plus the test that proves each one was built.
It is a sibling of `ait` (issues) and `ant` (decisions), and works without
them.

The records:

- **Feature**: a long-lived part of the app, such as "Exporting reports".
- **Story**: one piece of work on a feature, such as "add xlsx alongside
  csv". It holds the scenarios that piece of work adds, in order.
- **Scenario**: one example of behaviour, in Given/When/Then form. A test
  closes it.
- **Question**: something for the user to decide. It holds the story until
  the user says yes.

One session agrees a story with the user (the aat-writing skill). A fresh
session, you, then builds it.

## Building a story

You will be given a story id. Start with `aat story show <story>` and read
the story's description and all its scenarios. The description is the whole
picture: you see every scenario from the start so you don't paint yourself
into a corner. aat hands them out one at a time to keep the pace steady.

If the story shows `retires`, it drops existing behaviour. `next` hands
each one out before the story's new scenarios (see below).

`steps` is the story in order: each step is `{"scenario": ...}` or, for a
planned check-in, `{"question": ...}`, open or settled (a settled one has
the note of what was decided). Closed scenarios show the test that closed
them, so if you pick up a story someone else started, this tells you what
is done; `git diff` tells you what code they changed. `questions` lists
only open questions raised mid-build.

If you are given several stories, build them in the order the briefing
gives. aat doesn't know that one story depends on another; the briefing
does.

Before you change anything, run the project's whole test suite and note
the result. That is your baseline: if something fails later, you can tell
whether you broke it or it was already broken. If the baseline isn't
green, tell the user before you start.

Then loop:

1. Run `aat next --story <story>`. Only the builder runs `next`: it marks
   the scenario it hands you as started, which stops anyone editing it
   under you.
2. If it returns `{"scenario": ...}`, build that scenario test first:
   - Write one test for it, run it, and see it fail.
   - If it passes straight away (the behaviour came free with earlier work),
     show that it can fail: break the code on purpose, see the test go red,
     then put the code back. A test that has never failed proves nothing.
     This is the step most likely to leave the app broken, so make the
     break one small edit with the Edit tool, undo it with the same tool,
     run the test again to see it pass, and check `git diff` shows only the
     changes you meant to make. Don't copy files aside to restore later.
   - Write the code to make it pass, and run it again to see it pass.
   - Don't write tests for later scenarios ahead of time. The pace of one
     scenario at a time is the point, and only you can keep it.
   - Name the test after the scenario where the framework allows, so a
     reviewer can match them up.
   - "Nobody else is emailed" (or changed) is about the moment in the
     scenario's When. Its Given may send things of its own, so start
     counting after the Given, not from the start of the test.
   - Run the whole suite, not just the new test, and compare it with your
     baseline. aat only knows about tests written through it: in an app
     built before aat, the old rules live in old tests that nothing points
     you to. If an old test now fails because it asserts behaviour this
     story has just changed, update it as part of the change and say so
     in the note when you close or resolve. Any other new failure is
     yours to fix before closing.
   - Close it: `aat close <scenario> --test-file <file> --test "<test name>"`.
     Close only after the test run itself has passed: chain it on the test
     command's exit code (`<run the tests> && aat close ...`), never on
     reading its output. A close that went through on "1 failed" is a false
     record, however quickly the test is fixed.
   - If the scenario has `replaces`, it changes an existing rule. `replaces`
     shows the old rule, why it changed, and the test that proved it. Write
     the new test first as usual, then change the code and change or remove
     the old test. `aat close` warns if the old test name is still in its
     file.
3. If it returns `{"retirement": ...}`, remove that behaviour and its test.
   The test is the one named on the scenario, or, if it was never built,
   the one under its `replaces`. Run the remaining tests to see nothing else
   relied on it. Then close it with no test:
   `aat close <scenario>`. aat checks the old test name is gone from its
   file and refuses while it is still there.
4. If it returns `{"question": ...}`, stop building and put the question to
   the user (see below). Don't carry on with other scenarios meanwhile.
5. If it returns `{"scenario": null}`, the story is finished. Run the
   whole suite one last time and compare it with your baseline. If you
   noticed gaps along the way, raise them now as questions; they hold the
   story again until settled. Then tell the user it is ready for review,
   with the suite's result. Start that message with the calls you made
   yourself along the way, then anything the user has
   not yet seen working (a screen, an email), each with a link they can
   open, so it can't be lost further down.
   `aat export <story> --output .aat/<story>-review.md` writes a markdown
   review of the whole story: each scenario with the test that claims to
   prove it, and every question with the user's answers. Keep it inside
   `.aat/`, which is gitignored, so it doesn't land in the project.

### Where the project keeps its rules

House rules (how things are built, as opposed to what the app does) live
with the project, usually in CLAUDE.md or AGENTS.md. But some projects
generate that file with a tool that rewrites it, and a rule written straight
into it is lost on the next rewrite. Before adding or changing a rule, check
whether the file is generated, and if so, put the rule in the tool's own
source file, plus the generated file so it applies now. Signs:

- The file opens with a tool's marker or says it is generated. Laravel
  Boost's CLAUDE.md starts with `<laravel-boost-guidelines>`, has a
  `boost.json` beside it, and takes project rules from `.ai/guidelines/*`.
- A config file for an agent-guidance tool sits at the project root.
  Similar tools are reported for other stacks (Django AI Boost, Rails
  Hyperdrive, Next.js agent support, .NET Aspire's `aspire agent init`);
  none of these were checked, so read the tool's docs to find where its
  custom rules go.
- If you can't tell, ask the user.

Follow the project's own conventions while you build (its CLAUDE.md, its
component library, its test style). A reviewer checks those as well as the
tests, and a hand-rolled copy of a real component is a fail even when every
test is green. If a dependency update regenerates the house rules (Laravel
Boost does), read them again.

Write project files with your file-editing tools, not shell commands
(`cat >`, `tee`, `sed -i`). Projects often check edits through those tools,
and a file written from the shell skips the checks without anyone meaning
it to.

### What `aat close` checks

`aat close` checks only that the test file exists inside the project and
contains the test name as text, as a whole name: `test_adding_an_item` isn't
found inside `test_adding_an_item_twice`. Its output says "found the test
name in the file". It does not run the test, which is why you close only on
a passing run. Never describe a scenario as "verified": running the test is
the reviewer's job. If the same test already closes another scenario,
`close` still works but warns you; make sure the test really proves both.

Because the check is a text search, `--test` must match the name as it is
written in the file. A name with an escaped quote, such as
`'adding a customer\'s address'` in PHP, holds the backslash in the file
text, so `close` won't find "adding a customer's address". Pick a quoting
style that needs no escapes (double quotes, here), or pass the name exactly
as written.

### Don't change the agreed spec yourself

Don't add, change, reorder or regroup scenarios on your own, even your own
unbuilt ones. If something in the spec is wrong or missing, that is a
question for the user, and the session agreeing the story with them makes
the edit.

## Questions

Specs are rarely perfect. But being asked to approve things the builder
could have decided wears the user out: in the fourth trial it added up to
"I might as well have done this myself". So sort each gap first.

**Make the ordinary calls yourself.** How something is built, checking
input on the server (always), an awkward case with a sensible answer that
fits the story, wording nobody will notice, a small change the story
plainly implies: decide, build it, and keep a list. The list goes at the
top of your message when the story is finished, so the user can still
object.

**Raise a question** when the answer changes what people using the app see
or do, with no obvious right answer, or would be costly to undo, or goes
against what the story says ("nobody said what happens with a duplicate
name"). Don't quietly handle that kind of gap, and don't ignore it.

Questions include:

- A change that no scenario covers and the story doesn't plainly imply,
  such as a new screen. Ask before making it.
- Existing behaviour next to the story that now disagrees with it (the
  story says a blank import cell leaves an address alone, but the old code
  still wipes the name on a blank cell).
- Two scenarios that could each be read as proving the other. Ask which
  one is meant, rather than picking a reading.
- A new dependency (a package, a library, an upgrade). It needs the user's
  yes before you add it.

```bash
aat question ask --story <story> --body "When someone saves a second report with the same name, should we refuse it, or add (2) to the name? I'd suggest refusing it with a message."
```

Write the question about what the app does, in plain words, with an example,
and say what you would suggest. Don't ask the user about aat's records
("should this be a new scenario?").

A question can also come from the plan itself: `next` returns a question with
a `position` when a check-in was planned at that point in the story ("show
the user the export menu"). Treat it exactly like a question you raised.

Before you show the user anything to look at (a check-in, or a change after
a yes), look at it yourself first: take a screenshot and have a fresh agent,
with no part in the build, compare it with the app's other screens and say
what looks off. Fix what it finds. In the fourth trial the user found the
driver's screen ugly twice, which a look first would have caught.

Before you call something a bug, check the data behind it. A hint showing an
unexpected number may be the real value, changed by the user while trying
the app.

### Going round until the user says yes

1. Put the question to the user.
2. Record each reply in the user's own words:
   `aat question answer <question> --body "<their reply>"`.
3. Try what they ask for, and show them the result working. Repeat as often
   as it takes. The user may change their mind, go back to an earlier
   attempt, or throw it all out.
4. If a round changes behaviour, stay test first: write the new scenario's
   Given/When/Then and a failing test, make it pass, then show the user. If
   the user changes their mind, change the test with the code. A tweak with
   no change of behaviour ("right-align the header") needs no scenario and
   no test.
5. Only the user says yes. Silence or "hmm" is not yes. aat can't tell who
   typed a command, so this is a working agreement, not a lock. If the
   user says yes to your suggestion before you have built it, and there is
   nothing new for them to see (a change to stored data, say), that yes
   stands: build it test first, then resolve, and say in the note that the
   user agreed before it was built. If there is something to see (a
   screen, an email), a yes before it was built is not the last word:
   build it, put it in front of the user with a link they can open, and
   keep the question open until they have seen it and said yes again.

### When another session is coordinating

If your instructions came as a message from another Claude session (Claude
Code marks it as coming from another session, with that session's name),
that session is coordinating the work, and it is where the user is
talking. Questions then go through it:

1. Raise the question in aat as usual (or note the check-in `next` handed
   you). Then message the coordinating session, replying to the address
   your instructions came from, with the story id, the question id, and
   one line on what it is about. Whenever there is something to look at
   (a check-in, or a change you built after a yes), say what and how to
   reach it (a URL their phone can open, say).
2. Wait. The coordinating session puts the question to the user and
   records each reply with `aat question answer`. When it tells you to
   carry on, read the answers with `aat question show <question>`.
3. An answer recorded in aat is the user's answer, whoever typed the
   command. Claude Code's rule that a message from another session is not
   your user's approval is about permission prompts; it never lets you
   skip one. It doesn't stop you acting on an answer recorded in aat. In
   the same way, a coordinator saying the user allows a command doesn't
   change your own permission settings: if your session refuses it, say
   so, and tell the coordinator how the user can run or allow it.
4. You still do the rounds' work (trying things, tests first) and resolve
   the question yourself once the user has said yes.
5. If your message can't be delivered (the coordinating session has
   closed), put the question to the user directly in your own session.
6. The user may still talk to you directly, for example by pasting in a
   side agent's note. Answer them, but don't let a decision live only in
   your session: raise it as a question in aat (or record a round on the
   open one) in the user's exact words, then message the coordinating
   session with what was decided. Messages from the user and from the
   coordinator can arrive out of order, so say which you are answering.

### Settling the question

When the user says yes, resolve the question with a short note of what was
decided and why, in the user's terms. The user doesn't approve each note
when it is written; they read them all in the review at the end
(`aat export`), so make each one stand on its own:

```bash
# A tweak: nothing joins the spec.
aat question resolve <question> --note "Right-aligned the header; it lined up badly on mobile."

# New behaviour, with its test already written and passing during the rounds.
# A short scenario can go in on stdin, with no file to write:
aat question resolve <question> --note "Duplicate names are refused; renaming silently confused people." \
  --test-file tests/Feature/ReportTest.php --test "refuses a duplicate report name" --scenarios - <<'EOF'
Scenario: Refusing a duplicate report name
  Given a saved report called "Sales"
  When I save another report called "Sales"
  Then I am told the name is taken
EOF
```

With `--test-file` and `--test`, the one new scenario arrives already closed,
so `next` won't hand it out again. If the answer adds more than one scenario,
resolve with `--scenarios` alone, then close each new scenario with
`aat close`.

If the answer changes an existing rule rather than adding one, add
`--replaces <old scenario>`: the old one stays in the record, marked
replaced, with your note as the reason. A rule you closed earlier in this
same story is replaced the same way, never edited: its test was written
against its words.

If a recorded answer or resolve note turns out to be wrong (a note that
describes a fix later replaced, words in an answer that weren't the
user's), correct it visibly rather than leaving it:

```bash
aat question correct <question> --round 2 --body "<the user's exact words>" --reason "Removed a summary that wasn't the user's."
aat question correct <question> --resolve-note --body "<what is now true>" --reason "Question X replaced this fix."
```

The old text, the reason and the time stay on the record, and
`question show` and the review show them.

Then go back to `aat next`.

## Gherkin that aat accepts

A block of one or more scenarios, without a `Feature:` line:

```gherkin
Scenario: Exporting an empty report
  Given a report with no rows
  When I open the export menu
  Then the export button is disabled
  And a note says there is nothing to export
```

- `Scenario:` (or `Example:`) with a name, then Given, When, Then, And or But
  lines.
- Free-form text under the `Scenario:` line is kept as its description. A
  scenario may have only a description, for things that don't fit the
  formula; it still needs a test to close it.
- Not supported yet, and refused rather than guessed at: Background, Rule,
  Scenario Outline and Examples, tags, data tables, doc strings, the `*`
  keyword, and text before the first scenario. A refused batch stores
  nothing.

## Commands for building

| Command | What it does |
| --- | --- |
| `aat story show <story>` | The story, its feature, its steps in order (scenarios with status and test, and planned check-ins), open questions raised mid-build, and the scenarios it retires |
| `aat next --story S` | A question asked mid-build if there is one, then a retirement to remove, otherwise the first open scenario or planned check-in; marks a scenario it hands out as started |
| `aat close <scenario> --test-file F --test NAME` | Close a scenario with the test that proves it |
| `aat close <scenario>` | Close a retired scenario once its behaviour and test are removed |
| `aat question ask --story S --body B` | Raise a question now; it holds the story |
| `aat question answer <question> --body B` | Record one round of the user's answer |
| `aat question show <question>` | The question with every round and any corrections |
| `aat question resolve <question> --note N [--scenarios G [--replaces SC] [--test-file F --test NAME]]` | Settle it |
| `aat question correct <question> (--round N \| --resolve-note) --body B --reason R` | Correct a recorded answer or resolve note, visibly |
| `aat scenario show <scenario>` | One scenario, with what it replaced or what replaced it |
| `aat scenario replace <scenario> --story S --reason R --body G` | Replace a closed rule with one new scenario |
| `aat feature show <feature>` | The feature's current scenarios, in order, and its retired ones |
| `aat export <story> [--output FILE]` | The story as a markdown review for a person (markdown, not JSON) |

The commands for agreeing and tidying a story (`story add`, `scenario add`,
`scenario edit`, `move`, `delete`, `question plan`, `story accept` and the
rest) belong to the aat-writing skill.

Text flags (`--description`, `--body`, `--note`, `--scenarios`) take literal
text, `@path` to read a file, or `-` to read stdin. Stdin is only read when
you pass `-`. Test file paths can be relative to where you run the command;
aat stores them relative to the project root.

Output is JSON on stdout. Errors go to stderr as
`{"error": {"code": "...", "message": "..."}}` with a non-zero exit code.
Codes: `usage` (64), `validation` (65), `not_found` (66), `conflict` (67),
`uninitialised` and `internal` (1). Branch on the code, not the message.
