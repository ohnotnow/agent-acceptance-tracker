package aat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The skills and agent are built into the binary (claudefiles.go), so
// install-skills puts this release's copies in place and version says when
// the installed ones differ. self-update is ported from ait's.

var testSkills = fstest.MapFS{
	"skills/aat/SKILL.md":  {Data: []byte("building skill v2\n")},
	"agents/aat-critic.md": {Data: []byte("critic v2\n")},
}

func install(t *testing.T, cfg installConfig) (string, error) {
	t.Helper()
	var out bytes.Buffer
	if cfg.files == nil {
		cfg.files = testSkills
	}
	if cfg.stdin == nil {
		cfg.stdin = strings.NewReader("")
	}
	cfg.stdout = &out
	err := installSkills(cfg)
	return out.String(), err
}

func readOr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestInstallingSkills(t *testing.T) {
	t.Run("the real build carries both skills and the critic agent", func(t *testing.T) {
		list, err := skillFilesFor(skillFiles, "x")
		if err != nil {
			t.Fatal(err)
		}
		var rels []string
		for _, f := range list {
			rels = append(rels, f.rel)
		}
		want := []string{"agents/aat-critic.md", "skills/aat-writing/SKILL.md", "skills/aat/SKILL.md"}
		if strings.Join(rels, " ") != strings.Join(want, " ") {
			t.Fatalf("embedded = %v, want %v", rels, want)
		}
	})

	t.Run("missing files are added and identical ones left alone", func(t *testing.T) {
		dir := t.TempDir()
		out, err := install(t, installConfig{claudeDir: dir})
		if err != nil || readOr(t, filepath.Join(dir, "skills/aat/SKILL.md")) != "building skill v2\n" {
			t.Fatalf("err %v, out:\n%s", err, out)
		}
		out, _ = install(t, installConfig{claudeDir: dir})
		if strings.Count(out, "unchanged") != 2 {
			t.Fatalf("second run:\n%s", out)
		}
	})

	t.Run("with no terminal, a changed file is left as it is and reported", func(t *testing.T) {
		dir := t.TempDir()
		install(t, installConfig{claudeDir: dir})
		mine := filepath.Join(dir, "agents/aat-critic.md")
		os.WriteFile(mine, []byte("my tuned critic\n"), 0o644)
		out, err := install(t, installConfig{claudeDir: dir})
		if err != nil || readOr(t, mine) != "my tuned critic\n" || !strings.Contains(out, "agents/aat-critic.md: differs, left as it is") {
			t.Fatalf("err %v, file %q, out:\n%s", err, readOr(t, mine), out)
		}
	})

	t.Run("at a terminal: N keeps it, d shows the diff and asks again, y replaces it", func(t *testing.T) {
		dir := t.TempDir()
		install(t, installConfig{claudeDir: dir})
		mine := filepath.Join(dir, "agents/aat-critic.md")
		os.WriteFile(mine, []byte("my tuned critic\n"), 0o644)

		out, _ := install(t, installConfig{claudeDir: dir, interactive: true, stdin: strings.NewReader("\n")})
		if readOr(t, mine) != "my tuned critic\n" || !strings.Contains(out, "kept your copy") {
			t.Fatalf("N: file %q, out:\n%s", readOr(t, mine), out)
		}
		out, _ = install(t, installConfig{claudeDir: dir, interactive: true, stdin: strings.NewReader("d\ny\n")})
		if readOr(t, mine) != "critic v2\n" || !strings.Contains(out, "-my tuned critic") || !strings.Contains(out, "+critic v2") || strings.Count(out, "[y/N/d]") != 2 {
			t.Fatalf("d then y: file %q, out:\n%s", readOr(t, mine), out)
		}
	})

	t.Run("input ending mid-prompt stops, and never counts as an answer", func(t *testing.T) {
		dir := t.TempDir()
		install(t, installConfig{claudeDir: dir})
		for _, f := range []string{"agents/aat-critic.md", "skills/aat/SKILL.md"} {
			os.WriteFile(filepath.Join(dir, f), []byte("mine\n"), 0o644)
		}
		_, err := install(t, installConfig{claudeDir: dir, interactive: true, stdin: strings.NewReader("")})
		if err != errAborted || readOr(t, filepath.Join(dir, "skills/aat/SKILL.md")) != "mine\n" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("--force replaces without asking, and --dry-run changes nothing", func(t *testing.T) {
		dir := t.TempDir()
		install(t, installConfig{claudeDir: dir})
		mine := filepath.Join(dir, "agents/aat-critic.md")
		os.WriteFile(mine, []byte("mine\n"), 0o644)
		out, _ := install(t, installConfig{claudeDir: dir, dryRun: true})
		if readOr(t, mine) != "mine\n" || !strings.Contains(out, "differs, would ask") {
			t.Fatalf("dry run: out:\n%s", out)
		}
		install(t, installConfig{claudeDir: dir, force: true})
		if readOr(t, mine) != "critic v2\n" {
			t.Fatalf("force: file %q", readOr(t, mine))
		}
	})

	t.Run("version says whether the installed skills match", func(t *testing.T) {
		dir := t.TempDir()
		if s := skillsStatus(testSkills, dir); !strings.Contains(s, "not installed") {
			t.Fatal(s)
		}
		install(t, installConfig{claudeDir: dir})
		if s := skillsStatus(testSkills, dir); !strings.Contains(s, "up to date") {
			t.Fatal(s)
		}
		os.WriteFile(filepath.Join(dir, "agents/aat-critic.md"), []byte("old\n"), 0o644)
		if s := skillsStatus(testSkills, dir); !strings.Contains(s, "agents/aat-critic.md") || !strings.Contains(s, "aat install-skills") {
			t.Fatal(s)
		}
	})

	t.Run("version, install-skills and self-update work outside a project", func(t *testing.T) {
		out, errOut, code := run(t, t.TempDir(), "", "version")
		if code != 0 || !strings.HasPrefix(out, "aat version dev\nskills: ") {
			t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
		}
		out, _, code = run(t, t.TempDir(), "", "self-update")
		if code != 0 || !strings.Contains(out, "hand-built") {
			t.Fatalf("self-update: exit %d, out %q", code, out)
		}
	})
}

// fakeRelease serves a GitHub "latest release" with one binary and its SHA256SUMS.
func fakeRelease(t *testing.T, tag string, binary []byte, sums string) string {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(ghRelease{TagName: tag, Body: "Fixes things.", Assets: []ghAsset{
				{Name: "aat-linux-amd64", URL: srv.URL + "/bin"},
				{Name: "SHA256SUMS", URL: srv.URL + "/sums"},
			}})
		case "/bin":
			w.Write(binary)
		case "/sums":
			io.WriteString(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/latest"
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func updateCfg(t *testing.T, apiURL, stdin string) (selfUpdateConfig, *bytes.Buffer, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "aat")
	os.WriteFile(target, []byte("old binary"), 0o755)
	var out bytes.Buffer
	return selfUpdateConfig{apiURL: apiURL, client: http.DefaultClient, target: target, goos: "linux", arch: "amd64",
		home: "/nowhere", stdin: strings.NewReader(stdin), stdout: &out, stderr: &out}, &out, target
}

func TestSelfUpdate(t *testing.T) {
	newBin := []byte("new binary")
	t.Run("it downloads the newer release, checks it, swaps it in and then checks the skills", func(t *testing.T) {
		withVersion(t, "v0.1.0")
		api := fakeRelease(t, "v0.2.0", newBin, sumOf(newBin)+"  aat-linux-amd64\n")
		cfg, out, target := updateCfg(t, api, "y\n")
		var checked string
		cfg.afterSwap = func(target string, _ io.Writer) { checked = target }
		if err := selfUpdate(cfg); err != nil {
			t.Fatalf("err %v, out:\n%s", err, out)
		}
		if readOr(t, target) != "new binary" || checked != target || !strings.Contains(out.String(), "v0.1.0 -> v0.2.0") || !strings.Contains(out.String(), "Fixes things.") {
			t.Fatalf("file %q, checked %q, out:\n%s", readOr(t, target), checked, out)
		}
	})

	t.Run("a checksum mismatch replaces nothing", func(t *testing.T) {
		withVersion(t, "v0.1.0")
		api := fakeRelease(t, "v0.2.0", newBin, sumOf([]byte("something else"))+"  aat-linux-amd64\n")
		cfg, _, target := updateCfg(t, api, "")
		cfg.yes = true
		if err := selfUpdate(cfg); err == nil || readOr(t, target) != "old binary" {
			t.Fatalf("err %v, file %q", err, readOr(t, target))
		}
	})

	t.Run("anything but yes at the prompt leaves it alone", func(t *testing.T) {
		withVersion(t, "v0.1.0")
		api := fakeRelease(t, "v0.2.0", newBin, sumOf(newBin)+"  aat-linux-amd64\n")
		cfg, _, target := updateCfg(t, api, "\n")
		if err := selfUpdate(cfg); err != nil || readOr(t, target) != "old binary" {
			t.Fatalf("err %v, file %q", err, readOr(t, target))
		}
	})

	t.Run("--check exits 0 when current, 1 when newer, 2 when the lookup fails", func(t *testing.T) {
		withVersion(t, "v0.2.0")
		for _, c := range []struct {
			tag  string
			want error
		}{{"v0.2.0", nil}, {"v0.3.0", exitCode(1)}} {
			cfg, _, _ := updateCfg(t, fakeRelease(t, c.tag, newBin, ""), "")
			cfg.check = true
			if err := selfUpdate(cfg); err != c.want {
				t.Fatalf("latest %s: err = %v, want %v", c.tag, err, c.want)
			}
		}
		cfg, _, _ := updateCfg(t, "http://127.0.0.1:1/latest", "")
		cfg.check = true
		if err := selfUpdate(cfg); err != exitCode(2) {
			t.Fatalf("unreachable: err = %v", err)
		}
	})

	t.Run("a go install copy is sent to go install with the full command path", func(t *testing.T) {
		withVersion(t, "v0.1.0")
		cfg, out, _ := updateCfg(t, fakeRelease(t, "v0.2.0", newBin, ""), "")
		cfg.target, cfg.gopath = "/home/x/go/bin/aat", "/home/x/go"
		if err := selfUpdate(cfg); err != nil || !strings.Contains(out.String(), "go install github.com/ohnotnow/agent-acceptance-tracker/cmd/aat@latest") {
			t.Fatalf("err %v, out:\n%s", err, out)
		}
	})

	t.Run("only vMAJOR.MINOR.PATCH counts as a release", func(t *testing.T) {
		for v, want := range map[string]bool{"v1.2.3": true, "dev": false, "8de4aaf-dirty": false, "v1.2": false, "v1.2.3-rc1": false} {
			if isRelease(v) != want {
				t.Fatalf("isRelease(%q) = %v", v, !want)
			}
		}
		if !isNewer("v0.10.0", "v0.9.0") || isNewer("v0.9.0", "v0.10.0") || isNewer("v1.0.0", "dev") {
			t.Fatal("isNewer compares as strings")
		}
	})
}
