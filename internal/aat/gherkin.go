package aat

import (
	"regexp"
	"strconv"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// ParsedScenario is one Gherkin scenario as aat stores it.
type ParsedScenario struct {
	Title       string
	Description string // Gherkin's free-form text under the Scenario line
	Lines       []Line
}

type Line struct {
	Keyword string `json:"keyword"`
	Text    string `json:"text"`
}

var supportedKeywords = map[string]bool{"Given": true, "When": true, "Then": true, "And": true, "But": true}

// The scenarios are parsed inside a one-line wrapper Feature, because the
// parser only reads whole documents. Error positions are shifted back by
// that line so they match the user's own text.
const wrapperLine = "Feature: aat\n"

var errPosition = regexp.MustCompile(`\((\d+):(\d+)\)`)

// parseScenarios turns a block of Gherkin scenarios into ParsedScenarios.
// Anything aat doesn't support yet is rejected rather than guessed at.
func parseScenarios(text string) ([]ParsedScenario, error) {
	if strings.TrimSpace(text) == "" {
		return nil, validationErr("no scenarios given")
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "Feature:") {
			return nil, validationErr("leave out the Feature line: scenarios are added to a story, which belongs to a feature")
		}
	}

	doc, err := gherkin.ParseGherkinDocument(strings.NewReader(wrapperLine+text), (&messages.Incrementing{}).NewId)
	if err != nil {
		msg := errPosition.ReplaceAllStringFunc(err.Error(), func(m string) string {
			p := errPosition.FindStringSubmatch(m)
			line, _ := strconv.Atoi(p[1])
			return "(" + strconv.Itoa(line-1) + ":" + p[2] + ")"
		})
		return nil, validationErr("%s", strings.TrimSpace(msg))
	}

	if strings.TrimSpace(doc.Feature.Description) != "" {
		return nil, validationErr("text before the first Scenario isn't supported; put notes in a scenario's description")
	}

	var out []ParsedScenario
	for _, child := range doc.Feature.Children {
		switch {
		case child.Background != nil:
			return nil, validationErr("Background isn't supported yet")
		case child.Rule != nil:
			return nil, validationErr("Rule isn't supported yet")
		}
		sc, err := convertScenario(child.Scenario)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	if len(out) == 0 {
		return nil, validationErr("no scenarios found")
	}
	return out, nil
}

func convertScenario(s *messages.Scenario) (ParsedScenario, error) {
	name := strings.TrimSpace(s.Name)
	if s.Keyword != "Scenario" && s.Keyword != "Example" {
		return ParsedScenario{}, validationErr("%s isn't supported yet (scenario %q)", s.Keyword, name)
	}
	if len(s.Tags) > 0 {
		return ParsedScenario{}, validationErr("tags aren't supported yet (scenario %q)", name)
	}
	if name == "" {
		return ParsedScenario{}, validationErr("every scenario needs a name after %q", s.Keyword+":")
	}

	sc := ParsedScenario{Title: name, Description: dedent(s.Description)}
	for _, st := range s.Steps {
		kw := strings.TrimSpace(st.Keyword)
		if !supportedKeywords[kw] {
			return ParsedScenario{}, validationErr("the %q keyword isn't supported (scenario %q); use Given, When, Then, And or But", kw, name)
		}
		if st.DataTable != nil || st.DocString != nil {
			return ParsedScenario{}, validationErr("data tables and doc strings aren't supported yet (scenario %q)", name)
		}
		sc.Lines = append(sc.Lines, Line{Keyword: kw, Text: strings.TrimSpace(st.Text)})
	}
	if len(sc.Lines) == 0 && sc.Description == "" {
		return ParsedScenario{}, validationErr("scenario %q has no Given/When/Then lines and no description", name)
	}
	return sc, nil
}

func dedent(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}
