---
name: aat
description: Build a piece of work from an agreed Gherkin spec, one scenario at a time, test first, with aat (agent acceptance tracker). Use when a briefing or the user gives you an aat story to build, when you run `aat next`, close a scenario with its test, or raise, answer or resolve a question for the user. Also use when agreeing a new story with the user as Given/When/Then scenarios. You must use this skill if the user or a briefing mentions aat.
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

There are two jobs. One session agrees a story with the user. A fresh session
then builds it. Most of this skill is for the building session.

## Building a story

You will be given a story id. Start with `aat story show <story>` and read
the story's description and all its scenarios. The description is the whole
picture: you see every scenario from the start so you don't paint yourself
into a corner. aat hands them out one at a time to keep the pace steady.

If the story shows `retires`, it drops existing behaviour: remove that
behaviour and the test named on each retired scenario as part of the story.
`next` doesn't hand these out, so do them alongside the scenarios.

Then loop:

1. Run `aat next --story <story>`.
2. If it returns `{"scenario": ...}`, build that scenario test first:
   - Write one test for it, run it, and see it fail.
   - If it passes straight away (the behaviour came free with earlier work),
     show that it can fail: break the code on purpose, see the test go red,
     then put the code back. A test that has never failed proves nothing.
   - Write the code to make it pass, and run it again to see it pass.
   - Don't write tests for later scenarios ahead of time. The pace of one
     scenario at a time is the point, and only you can keep it.
   - Name the test after the scenario where the framework allows, so a
     reviewer can match them up.
   - Close it: `aat close <scenario> --test-file <file> --test "<test name>"`.
   - If the scenario has `replaces`, it changes an existing rule. `replaces`
     shows the old rule, why it changed, and the test that proved it. Write
     the new test first as usual, then change the code and change or remove
     the old test. `aat close` warns if the old test name is still in its
     file.
3. If it returns `{"question": ...}`, stop building and put the question to
   the user (see below). Don't carry on with other scenarios meanwhile.
4. If it returns `{"scenario": null}`, the story is finished. If you noticed
   gaps along the way, raise them now as questions; they hold the story
   again until settled. Then tell the user it is ready for review.
   `aat export <story> --output review.md` writes a markdown review of the
   whole story: each scenario with the test that claims to prove it, and
   every question with the user's answers.

Follow the project's own conventions while you build (its CLAUDE.md, its
component library, its test style). A reviewer checks those as well as the
tests, and a hand-rolled copy of a real component is a fail even when every
test is green.

### What `aat close` checks

`aat close` checks only that the test file exists inside the project and
contains the test name as text, as a whole name: `test_adding_an_item` isn't
found inside `test_adding_an_item_twice`. Its output says "found the test
name in the file". Never describe a scenario as "verified": running the test is the
reviewer's job. If the same test already closes another scenario, `close`
still works but warns you; make sure the test really proves both.

### Don't change the agreed spec yourself

Don't add, change, reorder or regroup scenarios on your own. If something in
the spec is wrong or missing, that is a question for the user.

## Questions

Specs are rarely perfect. When you hit a genuine gap ("nobody said what
happens with a duplicate name"), raise a question. Don't quietly handle the
gap, and don't ignore it.

```bash
aat question ask --story <story> --body "When someone saves a second report with the same name, should we refuse it, or add (2) to the name? I'd suggest refusing it with a message."
```

Write the question about what the app does, in plain words, with an example,
and say what you would suggest. Don't ask the user about aat's records
("should this be a new scenario?").

A question can also come from the plan itself: `next` returns a question with
a `position` when a check-in was planned at that point in the story ("show
the user the export menu"). Treat it exactly like a question you raised.

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
   typed a command, so this is a working agreement, not a lock.

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
replaced, with your note as the reason.

Then go back to `aat next`.

## Agreeing a story with the user

This is for the session that writes the spec with the user, before a fresh
session builds it.

```bash
aat init                                              # once per project; gitignores .aat/
aat feature list
aat feature add --title "Exporting reports" --description @feature.md
aat story add --feature <feature> --title "Add xlsx alongside csv" --description @story.md
aat scenario add --story <story> --body @scenarios.feature
aat question plan --story <story> --body "Show the user the export menu. Does the layout look right?"
aat scenario add --story <story> --body @more-scenarios.feature
```

- The story description is what the building session reads first: what is
  wanted and why, and anything known that doesn't fit a scenario.
- Scenarios are handed out in the order they were added.
- `question plan` puts a check-in after everything added so far. The builder
  stops there and asks the user, however well the build is going.
- Write scenarios about behaviour a user can see, in the user's language.
  Leave out implementation words (component names, routes, classes): the
  spec should survive a change of framework, where only the tests get
  rewritten.
- Brief the fresh session with the story id, and tell it to use this skill.

### Changing behaviour that already exists

The spec is the standing record of what the app does, so a later change is
a new story that changes the record, not an edit. Start with
`aat feature show <feature>` to see the current rules. Then, in the new
story:

```bash
aat scenario replace <old scenario> --story <story> --reason "Customers abroad have no UK postcode." --body @new-rule.feature
aat scenario retire <old scenario> --story <story> --reason "We no longer post anything to customers."
```

Nothing is deleted. The old scenario stays in its original story, marked
`replaced` or `retired`, with the reason. `feature show` and `publish` give
only the current rules, and each replacement carries what it replaced and
why, so a future agent finds the current rule, the old one, and the reason.

### Gherkin that aat accepts

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

## Commands

| Command | What it does |
| --- | --- |
| `aat story show <story>` | The story, its feature, its scenarios in order (status, test, position) and its open questions |
| `aat next --story S` | A question asked mid-build if there is one, otherwise the first open scenario or planned check-in |
| `aat close <scenario> --test-file F --test NAME` | Close a scenario with the test that proves it |
| `aat question ask --story S --body B` | Raise a question now; it holds the story |
| `aat question plan --story S --body B` | Plan a check-in after everything added so far |
| `aat question answer <question> --body B` | Record one round of the user's answer |
| `aat question show <question>` | The question with every round |
| `aat question resolve <question> --note N [--scenarios G [--replaces SC] [--test-file F --test NAME]]` | Settle it |
| `aat feature show <feature>` | The feature's current scenarios, in order, and its retired ones |
| `aat scenario show <scenario>` | One scenario, with what it replaced or what replaced it |
| `aat scenario replace <scenario> --story S --reason R --body G` | Replace a current scenario with one new scenario in a story |
| `aat scenario retire <scenario> --story S --reason R` | Retire a current scenario as part of a story |
| `aat feature add`, `feature list`, `story add`, `scenario add` | Agreeing a story, as above |
| `aat export <story> [--output FILE]` | The story as a markdown review for a person (markdown, not JSON) |
| `aat publish [--feature F] [--dir D]` | Write each feature as a `.feature` file for Gherkin tools (default `features/` at the project root). Every file is read back with Cucumber's parser first; a file aat didn't write is never overwritten |

Text flags (`--description`, `--body`, `--note`, `--scenarios`) take literal
text, `@path` to read a file, or `-` to read stdin. Stdin is only read when
you pass `-`. Test file paths can be relative to where you run the command;
aat stores them relative to the project root.

Output is JSON on stdout. Errors go to stderr as
`{"error": {"code": "...", "message": "..."}}` with a non-zero exit code.
Codes: `usage` (64), `validation` (65), `not_found` (66), `conflict` (67),
`uninitialised` and `internal` (1). Branch on the code, not the message.
