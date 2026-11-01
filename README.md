# aat: agent acceptance tracker

> Status: proof of concept. The commands and the database schema are still
> changing.

`aat` is a small command-line tool for building software with a coding
agent from an agreed spec. The spec is written in Gherkin, the
Given/When/Then format used in BDD (behaviour-driven development). The agent
builds it one scenario at a time, test first, and closes each scenario by
naming the acceptance test that proves it.

It's a sibling of [ait](https://github.com/ohnotnow/agent-issue-tracker),
which tracks the work, and `ant`, which records the decisions. `aat` holds
the agreed behaviour.

```
$ aat scenario add --story jOYboa --body @signup.feature
{"scenarios":[{"id":"MZY195f7","title":"A postcode is required"}]}

$ aat next --story jOYboa
{"scenario":{"id":"MZY195f7","story_id":"jOYboa","title":"A postcode is required","lines":[{"keyword":"Given","text":"I am signing up"},{"keyword":"When","text":"I leave the postcode blank"},{"keyword":"Then","text":"I am told the postcode is required"}],"position":1,"status":"open","created_at":"2026-11-01T22:51:37Z"}}

$ aat close MZY195f7 --test-file tests/SignUpTest.php --test "requires a postcode"
{"check":"found the test name in the file","id":"MZY195f7","status":"closed","test":"requires a postcode","test_file":"tests/SignUpTest.php"}

$ aat next --story jOYboa
{"scenario":null}
```

## Why it exists

Ask an agent to build a feature and it tends to race ahead. There's also
rarely a record of what the app is meant to do, kept apart from how it does
it.

`aat` sits between a full up-front spec and making it up as you go. The spec
stays as the record of what the app does.

## How it works

The records are features, stories and scenarios. A feature is a part of the
app that lasts, such as "Signing up". A story is one piece of work on a
feature, such as "make the postcode optional". A scenario is one
Given/When/Then example of behaviour.

1. You and an agent agree a story, writing the piece of work down as
   scenarios. You can plan check-ins between them: points where the agent
   must stop and show you something before carrying on.
2. A fresh agent session builds it. It runs `aat next` to get the next
   scenario, writes a failing test, makes it pass, and closes the scenario
   with `aat close`.
3. When the agent finds a gap in the spec, it raises a question with
   `aat question ask`. The question holds the story until it is resolved.
   The agreement is that only you resolve it, once you've seen the result
   working. aat can't tell who typed a command, so it doesn't enforce that.
4. When `aat next` has nothing left, `aat export` writes the story as a
   markdown review.
5. A later story can replace or retire a scenario. The old scenario stays in
   the record, marked, with the reason.

`aat close` only checks that the test file is inside the project and
contains the test name. It doesn't run your tests; that's the reviewer's
job.

The [skill](claude/skills/aat/SKILL.md) in this repo explains the workflow
to a Claude Code agent.

## Gherkin support

Scenarios are parsed with Cucumber's own Go parser. `aat` accepts
`Scenario:` (or `Example:`) with a name, Given/When/Then/And/But lines, and
free-form description text under the scenario line. Anything it doesn't
support yet is refused: Background, Rule, Scenario Outline and Examples,
tags, data tables, doc strings and the `*` keyword.

`aat publish` writes each feature as a `.feature` file (in `features/` by
default), with an `@aat-<id>` tag on each scenario. It reads every file back
with the same parser before writing anything, and never overwrites a file it
didn't write itself.

`aat` isn't a test runner. To run the published files as tests, bind them to
your code with Cucumber, Behave or pytest-bdd as you normally would.

## Prerequisites

- Go 1.27.1 or later

## Getting started

Clone the repository, then build and install the `aat` binary:

```bash
go install ./cmd/aat
```

Then, in the project you want to use it in:

```bash
aat init
```

`aat init` creates `.aat/aat.db` at the root of the git repository and adds
`.aat/` to `.gitignore`.

Copy `claude/skills/aat/` into `~/.claude/skills/`.

## Commands

| Command | What it does |
| --- | --- |
| `aat init` | Set up aat in this project |
| `aat feature add --title T [--description D]` | Add a feature |
| `aat feature list` | List features |
| `aat feature show <feature>` | A feature's current scenarios, and its retired ones |
| `aat story add --feature F --title T [--description D]` | Add a story to a feature |
| `aat story show <story>` | A story with its scenarios and open questions |
| `aat scenario add --story S --body B` | Add Gherkin scenarios to a story |
| `aat scenario show <scenario>` | One scenario, with what it replaced or what replaced it |
| `aat scenario replace <scenario> --story S --reason R --body B` | Replace a scenario with a new one, as part of a story |
| `aat scenario retire <scenario> --story S --reason R` | Retire a scenario, as part of a story |
| `aat next --story S` | What to build or ask next |
| `aat close <scenario> --test-file F --test NAME` | Close a scenario with the test that proves it |
| `aat question ask --story S --body B` | Raise a question; it holds the story until resolved |
| `aat question plan --story S --body B` | Plan a check-in after the story's scenarios so far |
| `aat question answer <question> --body B` | Record one round of the user's answer |
| `aat question show <question>` | A question with all its rounds |
| `aat question resolve <question> --note N [--scenarios G [--replaces SC] [--test-file F --test NAME]]` | Settle a question, optionally adding scenarios; one new scenario can replace a current one, or arrive closed with its test |
| `aat publish [--feature F] [--dir D]` | Write features as `.feature` files |
| `aat export <story> [--output FILE]` | The story as a markdown review |
| `aat version` | Print the version |

Text flags take literal text, `@path` to read a file, or `-` to read
stdin. Output is JSON on stdout, apart from `export`, which writes markdown.
Errors go to stderr as `{"error": {"code": "...", "message": "..."}}`, with
exit codes 64 (usage), 65 (validation), 66 (not found), 67 (conflict) and 1
(internal, or not initialised).

## Running tests

```bash
go test ./...
```

## Contributing

Fork or clone the repository, make your change with a test, and check
`go test ./...` passes before sending it over.

## Licence

MIT. See [LICENSE](LICENSE).
