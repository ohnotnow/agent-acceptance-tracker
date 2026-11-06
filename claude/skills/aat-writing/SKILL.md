---
name: aat-writing
description: Agree a piece of work with the user as Gherkin scenarios in aat (agent acceptance tracker), have it attacked before anyone builds it, brief and coordinate a builder session, and review the finished story. The user starts this with /aat-writing. A session building a story uses the aat skill instead.
disable-model-invocation: true
---

# aat-writing: agree a story, check it, coordinate the build

`aat` holds what an app is meant to do, written as Gherkin scenarios
(Scenario, Given/When/Then), plus the test that proves each one was built.
The records:

- **Feature**: a long-lived part of the app, such as "Exporting reports".
- **Story**: one piece of work on a feature, such as "add xlsx alongside
  csv". It holds the scenarios that piece of work adds, in order.
- **Scenario**: one example of behaviour, in Given/When/Then form. A test
  closes it.
- **Question**: something for the user to decide. It holds the story until
  the user says yes.

This skill is for the session that talks to the user: agreeing the story,
checking it, briefing a fresh builder session, relaying its questions, and
reviewing the result. The builder follows the aat skill.

The spec is what the user agreed, and the build can only be as good as the
words in it. Nearly every mid-build question in past trials could have been
caught here, by reading the story against the code before anyone built it.

The user's time is the scarce thing. In the fourth trial the agreeing
session asked about a hundred questions one at a time, and the user said
the whole stage added up to "I might as well have done this myself". The
flow below exists to stop that: you and a critic agent do the arguing, and
the user sees the result.

## How a story gets agreed

1. **Get the gist.** A short chat about what the user wants and why. Unless
   they ask for a long back-and-forth, take the gist and go: don't
   interview them.
2. **Draft the whole story fast.** Read the code (below), then write the
   story, its description, every scenario and any check-ins into aat in
   one go. Make the ordinary calls yourself and write them in.
3. **Have it attacked.** Run the `aat-critic` agent on it (below), argue
   with its findings, fix the story, and run it once more.
4. **Show the user one short list.** What the story now does, in a few
   lines; then only the decisions that are genuinely theirs, each with your
   position and one real example; then, in a line or two, what the critic
   caught that you fixed. Apply their answers, then brief the builder.

**What is the user's call:** something people using the app will see or
do differently, with no obvious right answer; anything costly to undo;
anything that contradicts what the user said. **What isn't:** how it is
built, checking input on the server (always), obvious awkward cases with a
sensible answer, and wording nobody will notice. Decide those, write them
into the story, and let them show up in the summary, where the user can
still object.

## Talking to the user

- Plain words and real examples. The user may know nothing about BDD or
  Gherkin beyond the Given/When/Then shape: describe what a decision
  changes in the app, with one example, never in BDD terms.
- Give a position, not a menu: "I'd do X, because..., does that hold for
  you?"
- Short replies. No walls of text, no stream of links, no decisions the
  user doesn't need to make.
- Every noun needs a referent the user can see. They can't see the
  builder's messages, side agents' notes in other terminals, or your own
  reasoning.
- Before you state what a file, a skill or the code says, look. "The skill
  already says that" and "that's a display bug" have both been wrong when
  asserted from memory.

## Agreeing a story

```bash
aat init                                              # once per project; gitignores .aat/
aat feature list
aat feature add --title "Exporting reports" --description @feature.md
aat story add --feature <feature> --title "Add xlsx alongside csv" --description @story.md
aat scenario add --story <story> --body @scenarios.feature
aat question plan --story <story> --body "Show the user the export menu. Does the layout look right?"
aat scenario add --story <story> --body @more-scenarios.feature
```

### Read the code first

Before writing scenarios, read the code for every place the change touches:
each screen and form, previews and confirmation pages, badges, labels and
colours, imports, exports and downloads, emails, listeners and jobs,
policies, sample data and test factories, existing tests, and the docs.
Give each one a scenario, or say in the story description that it is
deliberately left alone. Anything not covered is a guess for the builder.

Before calling something left alone, check it still means the same after
the change, not just that its code is untouched: a "notified at" column
nobody edits can still end up holding the same time for everyone, and a
"completed" count can stop meaning "finished" once stops can end another
way.

### Writing scenarios that hold up

A scenario only promises what it says, and a test written against it can
pass while the point is missed. Check each one against these:

- **Say what must not happen.** "Dan is emailed" passes while Cat is
  emailed too. When a moment sends or changes several things, write one
  scenario for that moment listing all of them, then "and nobody else is
  emailed" (or the equivalent). Two scenarios about the same moment, each
  saying "nobody else", contradict each other. A repeated action (a double
  tap) says "nobody is emailed", not "Bob isn't emailed again", which lets
  the next customer get a duplicate.
- **"Every" passes on none.** "Every email sent says X" is true when no
  emails are sent. Name the expected set: "six emails are sent: Ann's...,
  then every one of them says X".
- **A fallback says which value must not appear.** If a run's own number
  wins over the business's, add "and no email's link rings the business's
  number"; an email showing both would otherwise pass.
- **To prove a value is looked up rather than copied, go through the real
  route.** "Changing the business's number reaches runs already set up" is
  only proved if the run in the Given was made on the real form; a test
  that creates the run directly passes even when the form copies the
  number.
- **Pin down values that get used for something.** Wherever a value feeds
  a link, a calculation, a lookup or an import, say what a valid one looks
  like and what happens to an invalid one, on every route it arrives by.
  Loose words like "free text" for a phone number that a tap-to-call link
  is then built from cost three mid-build questions and a dependency
  upgrade. Even "when the user enters a valid-looking phone number" would
  have caught most of it. For every free-text field, follow it to each
  place it ends up (links, exports, imports, other screens) and ask
  whether free text still works there. Always validate user data on the
  server; a story need not ask the user whether to.
- **Symmetry.** If a run can have its own number and its own email, each
  needs its own scenario. If a value can be entered on two forms, each
  form needs its own refusal of a bad value.
- **Same data, several routes.** When the same data can arrive by more
  than one route (a form and an import, say), write a scenario that the
  routes record it identically, including when a field is left empty.
  What "empty" is stored as belongs in the project's own rules, not in
  the scenario.
- **Say where a result is seen.** "Then the run shows the address" could
  mean it is saved, or that it appears on a particular screen. Name the
  screen, or say "is saved". If a screen shows the same figure twice (a
  line under the title and a footer), say which, or both.
- **Awkward cases.** None, one, two and many; the first and the last (code
  that stops early when "no stops are left" can skip the last customer's
  email); things done out of order; the same action twice quickly; an
  action before the thing has started; zero counts in wording ("0 nobody
  in" on every normal run).
- **One scenario, one test.** If two scenarios would be proved by the same
  test (a Cancel button that only closes a panel and tells the app
  nothing), merge them, or give one of them something checkable.
- **Behaviour a user can see, in the user's language.** Leave out
  implementation words (component names, routes, classes): the spec should
  survive a change of framework, where only the tests get rewritten.

### What goes in the description, and what doesn't

The story description is what the builder reads first: what is wanted and
why, and anything that doesn't fit a scenario. Only scenarios get a test
and stay in the lasting record, so:

- **A rule anyone can see is a scenario**, never a description note: a page
  someone is turned away from, a link that can be tapped, a length limit,
  a label. If it is only in the description, the builder can miss it and
  the story still finishes all green.
- **The description holds building guidance**: what is deliberately left
  alone, the docs in scope (README, technical overview), test data
  defaults (a factory default that changes can silently change what old
  tests mean), sample data the check-ins need, things that follow from the
  design and must not be "fixed", and traps in the existing code.
- **A safety check nobody sees** (the server re-checking a driver's PIN on
  every tap) goes in the description, in terms of what the user never
  sees. Written as a scenario, it read to the user as the driver having
  to re-enter their PIN.

### Planned check-ins

`question plan` puts a check-in after everything added so far; the builder
stops there and asks the user, however well the build is going. In its
text, say what the user will be shown, and how they will actually see it:
anything that has to happen first (a migration, fresh sample data, a queue
worker running so emails are sent, a mail catcher's link), who runs
database commands under the house rules, and that every link must open on
the user's phone. Ask about each thing shown separately.

### A story belongs to one feature

A story only adds to, replaces or retires scenarios in its own feature;
aat refuses a replacement across features. If an agreed change reaches the
rules of another feature, put that part in a story on that feature (an
existing one in the same build, if there is one) and say so in both
descriptions.

### Several stories in one build

aat doesn't know that one story depends on another. The briefing names the
order. When the second story builds on the first, read the first story's
scenarios as the code that will exist, and say in the second's description
what it relies on.

## Having the draft attacked

Run the `aat-critic` agent with the project directory and the story id.
It knows nothing about how the story was agreed, which is the point: in
the fourth trial a side agent with no knowledge of the process found most
of the real problems, and a checklist-driven check that shared the
agreeing session's view found almost none.

1. **Round one.** It tries real, messy inputs, follows every value to where
   it ends up, and reports what goes wrong, each finding marked as a story
   fix, a builder's call or the user's call.
2. **Argue.** For each finding: fix the story (with the tidying commands
   below), or disagree with a reason, or keep it for the user. Don't accept
   a finding just because it was raised, and don't wave one off without
   looking at its evidence.
3. **Round two.** Send your replies to the same agent (continue it, so it
   keeps its context). It checks your fixes and pushes back once where it
   still disagrees. Then stop: two rounds, then the user.

Anything still disputed after round two goes on the user's list, with both
views in a sentence each. Side agents' notes and the user's own nose still
count: treat them as findings, not interruptions.

### Tidying a story before it is built

Until a step is built, it is a draft:

- `aat scenario edit` rewrites a scenario in place.
- `aat scenario add --before <step>` and `aat question plan --before <step>`
  put new work in front of a step (a new scenario that belongs before a
  check-in, say).
- `aat scenario move` and `aat question move` reorder.
- `aat question edit` rewrites a check-in or question nobody has answered.
- `aat scenario delete` drops a draft scenario that is no longer wanted
  (two merged into one), but not one that replaces an earlier rule or came
  out of the user's answer to a question: those are links in the record,
  so edit them instead.
- `aat story edit` changes the story's title or description until the
  story is accepted. If the builder has already started, tell it.

Use these for fixes before the build, not `scenario replace`, which would leave a
"replaced" record of something never built. aat refuses them once `aat
next` has handed a scenario to the builder (who may already have a test
against its words), once it is closed, or once a question has an answer.
From then on, changes go through a question or a replacement, which keep
the history.

## Briefing the builder

Brief a fresh session with the story ids, in order, and tell it to use the
aat skill. Say:

- whether it should stop between stories or build them all and report back;
- that it runs the whole suite first as a baseline, and doesn't commit;
- anything about the environment it can't guess (who may run database
  commands, a slow asset watcher to wait for before looking at a page);
- that its questions and check-ins come to you, if you are coordinating.

Write the brief imperatively. Some setups gate any message containing a
question mark, which can stop the builder acting on it.

## Coordinating the build

If you brief the builder by message from your own session, you are its
coordinator, and the user talks to you.

- **Only the builder runs `aat next`.** It marks the scenario it hands out
  as started, which stops it being edited. To see where a story is, use
  `aat story show`.
- **Relay in the user's terms.** The user never sees the builder's
  messages, and may not have looked at the app in months. Rewrite each
  point from what the user would see, with an example, never in the
  builder's words or code terms. If a point needs no decision, don't pass
  it on as one.
- **Don't relay what the builder can decide.** If a builder's question is
  an ordinary building call (see "What is the user's call" above), tell it
  to make the call and note it, rather than passing it to the user.
- **Read before relaying.** When the builder messages you about a
  question, read it (`aat question show`) and the story, and put it to
  the user with what is being built, what they are being asked, and your
  suggestion. If there is anything to look at, put it first, with its
  link.
- **A reply about one thing is not a yes to the rest.** When a check-in
  shows several things, ask about each. Silence or "mostly" is not yes:
  ask.
- **Record only the user's exact words** with `aat question answer`: no
  summary, no "(agreeing to ...)", no framing. Future readers treat an
  answer as the user's decision. If the reply only makes sense with what
  you asked, put that in your message to the builder, for its resolve
  note.
- **When an answer changes things under the builder**, edit the open
  drafts it affects yourself (scenarios and description), tell the builder
  which, and leave closed ones for it to replace through its resolve.
  Messages cross: say which of its messages you are answering.
- **Check the data before calling something a bug.** A hint showing an
  unexpected number may be the value the user typed while trying the app.
- **Permissions stay per session.** If the builder's session refuses a
  command, don't run it on its behalf and don't tell it to work round the
  refusal. Tell the user how to run or allow it in that session.
- **Keep a tally** of the builder's mid-build questions and where each
  came from. It is the honest measure of how well the story was agreed.

## Reviewing a finished story

When the builder says the story is finished:

1. Lead with anything the user has not yet seen working, each with a link.
2. Pass on the builder's own notes in full, briefly, with what you would do
   about each. Include anything it owned up to.
3. Run the whole suite. Read the diff and each scenario's test, and check
   the test proves what the scenario says, including anything it says
   must not happen.
4. Ask a fresh-eyes agent, with no part in the build, to read the diff
   against the story description, looking for anything whose meaning
   changed without a scenario saying so (a column, a status, a badge, a
   doc), and for docs that no longer match the app.
5. Raise each finding that needs work as a question on the story
   (`aat question ask`), in plain words with your suggestion, and record
   the user's answer. The builder gets them from `aat next`.
6. When the user is happy and nothing is left, accept the story with their
   words: `aat story accept <story> --note "..."`. Only the user accepts.
   An accepted story takes no more scenarios or questions; later changes
   are a new story.

Ideas that come up along the way and aren't for this story go into the
project's issue tracker, not aat: an aat question holds the story, which is
wrong for "someday".

## Changing behaviour that already exists

The spec is the standing record of what the app does, so a later change is
a new story that changes the record, not an edit. Start with
`aat feature show <feature>` to see the current rules. Then, in the new
story:

```bash
aat scenario replace <old scenario> --story <story> --reason "Customers abroad have no UK postcode." --body @new-rule.feature
aat scenario retire <old scenario> --story <story> --reason "We no longer post anything to customers."
```

The old scenario stays in its original story, marked `replaced` or
`retired`, with the reason. A replacement from another story goes at the
end of the new story; move it into place with `aat scenario move`.
`feature show` and `publish` give only the current rules, and each
replacement carries what it replaced and why, so a future agent finds the
current rule, the old one, and the reason.

## Where the project keeps its rules

House rules (how things are built) live with the project, usually in
CLAUDE.md or AGENTS.md. Some projects generate that file (Laravel Boost:
a `<laravel-boost-guidelines>` marker, a `boost.json`, rules sourced from
`.ai/guidelines/*`), and a rule written straight into it is lost on the
next rewrite. Put a new rule in the tool's own source file, plus the
generated file so it applies now. Standing rules belong there, not in a
story description or a handover.

## Gherkin that aat accepts

A block of one or more scenarios, without a `Feature:` line: `Scenario:`
(or `Example:`) with a name, then Given, When, Then, And or But lines.
Free-form text under the `Scenario:` line is kept as its description. Not
supported yet, and refused rather than guessed at: Background, Rule,
Scenario Outline and Examples, tags, data tables, doc strings, the `*`
keyword, and text before the first scenario. A refused batch stores
nothing.

## Commands for writing

| Command | What it does |
| --- | --- |
| `aat feature add --title T [--description D]` | Add a feature |
| `aat feature list` / `feature show <feature>` | List features; a feature's current scenarios and its retired ones |
| `aat story add --feature F --title T [--description D]` | Add a story to a feature |
| `aat story show <story>` | The story, its steps in order, open questions and retirements |
| `aat story edit <story> [--title T] [--description D]` | Change a story's title or description until it is accepted |
| `aat story accept <story> --note N` | Record that the user reviewed the finished story and accepted it |
| `aat scenario add --story S --body G [--before STEP]` | Add scenarios, at the end or in front of a draft step |
| `aat scenario edit <scenario> --body G` | Rewrite a draft scenario in place |
| `aat scenario move <scenario> --before STEP` | Move a draft scenario in front of another draft step |
| `aat scenario delete <scenario>` | Delete a draft scenario, unless it replaces a rule or came out of a question |
| `aat scenario replace <scenario> --story S --reason R --body G` | Replace a current rule with one new scenario |
| `aat scenario retire <scenario> --story S --reason R` | Retire a current rule |
| `aat scenario show <scenario>` | One scenario, with what it replaced or what replaced it |
| `aat question plan --story S --body B [--before STEP]` | Plan a check-in, at the end or in front of a draft step |
| `aat question edit <question> --body B` | Rewrite an open question nobody has answered |
| `aat question move <question> --before STEP` | Move an open check-in in front of another draft step |
| `aat question ask --story S --body B` | Raise a question (a review finding, say); it holds the story |
| `aat question answer <question> --body B` | Record one round of the user's exact words |
| `aat question show <question>` | A question with every round and any corrections |
| `aat question correct <question> (--round N \| --resolve-note) --body B --reason R` | Correct a recorded answer or note, visibly |
| `aat export <story> [--output FILE]` | The story as a markdown review |
| `aat publish [--feature F] [--dir D]` | Write features as `.feature` files for Gherkin tools |
| `aat publish --yaml [--feature F]` | Write everything about each feature to `features/yaml/`, for another developer to `aat import` |
| `aat import <file>...` | Merge a colleague's YAML file; on a clash it writes nothing and lists the records |

Text flags take literal text, `@path` to read a file, or `-` to read stdin.
Output is JSON on stdout; errors go to stderr as
`{"error": {"code": "...", "message": "..."}}`, with codes `usage`,
`validation`, `not_found`, `conflict`, `uninitialised` and `internal`.
