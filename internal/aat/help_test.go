package aat

import (
	"strings"
	"testing"
)

// Help is for a person at a terminal: plain text on stdout, exit 0, like
// ait and ant (ait aat-s18ja). Errors stay JSON on stderr for agents, but
// short, pointing at --help rather than carrying the usage text.

func TestAskingForHelp(t *testing.T) {
	t.Run("aat, aat --help, aat -h and aat help print the usage as plain text, even with no database", func(t *testing.T) {
		dir := t.TempDir()
		for _, args := range [][]string{{}, {"--help"}, {"-h"}, {"help"}} {
			out, errOut, code := run(t, dir, "", args...)
			if code != 0 || errOut != "" {
				t.Fatalf("aat %v: exit %d, stderr %q", args, code, errOut)
			}
			if !strings.HasPrefix(out, "usage: aat") || !strings.Contains(out, "\n  story agree <story> --note N") || strings.Contains(out, `\n`) {
				t.Fatalf("aat %v: stdout =\n%s", args, out)
			}
		}
	})

	t.Run("help on a command prints just that command's lines", func(t *testing.T) {
		dir := t.TempDir()
		for _, args := range [][]string{{"story", "agree", "--help"}, {"help", "story", "agree"}} {
			out, errOut, code := run(t, dir, "", args...)
			if code != 0 || errOut != "" {
				t.Fatalf("aat %v: exit %d, stderr %q", args, code, errOut)
			}
			if !strings.Contains(out, "story agree <story> --note N") || !strings.Contains(out, "next\n") && !strings.Contains(out, "refuses it") || strings.Contains(out, "story accept") {
				t.Fatalf("aat %v: stdout =\n%s", args, out)
			}
		}
		out, _, _ := run(t, dir, "", "story", "--help")
		for _, want := range []string{"story add", "story show", "story edit", "story agree", "story accept"} {
			if !strings.Contains(out, want) {
				t.Fatalf("aat story --help is missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "scenario add") || strings.Contains(out, "  stories ") {
			t.Fatalf("aat story --help has other commands:\n%s", out)
		}
	})

	t.Run("help after a command's arguments still finds the command", func(t *testing.T) {
		out, _, code := run(t, t.TempDir(), "", "story", "show", "XZaWlH", "--help")
		if code != 0 || !strings.Contains(out, "story show <story>") {
			t.Fatalf("exit %d, stdout =\n%s", code, out)
		}
	})

	t.Run("an unknown command is a usage error, with or without help or a database", func(t *testing.T) {
		dir := t.TempDir()
		for _, args := range [][]string{{"frobnicate", "--help"}, {"frobnicate"}} {
			if code := errorCode(t, dir, "", args...); code != "usage" {
				t.Fatalf("aat %v: code = %q, want usage", args, code)
			}
		}
	})

	t.Run("a usage error is short JSON that points at --help, not the whole usage text", func(t *testing.T) {
		dir := newProject(t)
		for _, args := range [][]string{{"frobnicate"}, {"--colour", "red"}, {"story", "show"}} {
			_, errOut, code := run(t, dir, "", args...)
			if code != 64 || !strings.Contains(errOut, `"code":"usage"`) || !strings.Contains(errOut, "aat --help") || strings.Contains(errOut, "story accept") {
				t.Fatalf("aat %v: exit %d, stderr %s", args, code, errOut)
			}
		}
	})
}
