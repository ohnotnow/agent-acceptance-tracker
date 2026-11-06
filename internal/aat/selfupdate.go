package aat

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// self-update, ported from ait's (internal/ait/cmd_self_update.go in
// agent-issue-tracker), keeping its safety properties: a hand-built binary
// is never replaced; the download is checked against the release's
// SHA256SUMS before anything is swapped; Homebrew and go install copies are
// sent to their own updaters; an unwritable install folder fails before
// downloading; the swap is a rename of a finished file.

type selfUpdateConfig struct {
	apiURL     string
	client     *http.Client
	target     string // the running binary
	goos, arch string
	gopath     string
	home       string
	check      bool
	yes        bool
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	// afterSwap runs once the new binary is in place. By default it asks the
	// new binary whether the skills changed too.
	afterSwap func(target string, out io.Writer)
}

func runSelfUpdateCmd(args []string, streams Streams) error {
	flags := newFlags("self-update")
	check := flags.Bool("check", false, "only report whether a newer release exists (exit 0 current, 1 newer, 2 lookup failed)")
	yes := flags.Bool("yes", false, "don't ask before updating")
	flags.BoolVar(yes, "y", false, "don't ask before updating")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the running aat: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	home, _ := os.UserHomeDir()
	return selfUpdate(selfUpdateConfig{
		apiURL: latestReleaseURL(RepoURL), client: &http.Client{Timeout: 60 * time.Second},
		target: exe, goos: runtime.GOOS, arch: runtime.GOARCH, gopath: os.Getenv("GOPATH"), home: home,
		check: *check, yes: *yes, stdin: streams.Stdin, stdout: streams.Stdout, stderr: streams.Stderr,
		afterSwap: checkNewSkills,
	})
}

func selfUpdate(cfg selfUpdateConfig) error {
	out := cfg.stdout
	if !isRelease(Version) {
		fmt.Fprintf(out, "aat %s is a hand-built copy, not a release, so self-update leaves it alone.\n", Version)
		fmt.Fprintf(out, "Rebuild it from source (make install), or install a release from %s/releases/latest\n", RepoURL)
		return nil
	}
	rel, err := fetchLatestRelease(cfg.client, cfg.apiURL)
	if err != nil {
		if cfg.check {
			fmt.Fprintf(out, "Couldn't find the latest release: %v\n", err)
			return exitCode(2)
		}
		return fmt.Errorf("find the latest release: %w", err)
	}
	if !isNewer(rel.TagName, Version) {
		fmt.Fprintf(out, "Already up to date (%s).\n", Version)
		return nil
	}
	if cfg.check {
		fmt.Fprintf(out, "aat %s is installed; %s is available.\n", Version, rel.TagName)
		return exitCode(1)
	}

	if manager, command := packageManager(cfg.target, cfg.gopath, cfg.home); manager != "" {
		fmt.Fprintf(out, "aat was installed with %s. %s is available; update with:\n  %s\n", manager, rel.TagName, command)
		return nil
	}
	asset := assetName(cfg.goos, cfg.arch)
	if asset == "" {
		return validationErr("there is no release build for %s/%s", cfg.goos, cfg.arch)
	}
	bin, ok := findAsset(rel.Assets, asset)
	if !ok {
		return notFoundErr("release %s has no %s", rel.TagName, asset)
	}
	sums, ok := findAsset(rel.Assets, "SHA256SUMS")
	if !ok {
		return notFoundErr("release %s has no SHA256SUMS, so the download can't be checked", rel.TagName)
	}
	dir := filepath.Dir(cfg.target)
	if !canWrite(dir) {
		return validationErr("can't write to %s: run it with sudo, or download from %s/releases/latest", dir, RepoURL)
	}

	if !cfg.yes {
		fmt.Fprintf(out, "aat %s -> %s\n", Version, rel.TagName)
		if notes := strings.TrimSpace(rel.Body); notes != "" {
			fmt.Fprintf(out, "\nRelease notes:\n%s\n", notes)
		}
		fmt.Fprint(out, "\nUpdate now? [y/N] ")
		line, _ := bufio.NewReader(cfg.stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Not updated.")
			return nil
		}
	}

	binBytes, err := download(cfg.client, bin.URL)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	sumsBytes, err := download(cfg.client, sums.URL)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := checksumFor(string(sumsBytes), asset)
	if err != nil {
		return err
	}
	got := sha256.Sum256(binBytes)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return validationErr("the download of %s doesn't match its checksum, so nothing was replaced", asset)
	}
	if err := swapIn(cfg.target, binBytes); err != nil {
		return fmt.Errorf("replace the binary: %w", err)
	}
	fmt.Fprintf(out, "Updated to %s.\n", rel.TagName)
	if cfg.afterSwap != nil {
		cfg.afterSwap(cfg.target, out)
	}
	return nil
}

// checkNewSkills asks the newly installed binary, which carries the new
// skills, how they compare with the installed ones.
func checkNewSkills(target string, out io.Writer) {
	fmt.Fprintln(out, "\nThe skills and agent may have changed too:")
	cmd := exec.Command(target, "install-skills", "--dry-run")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "(couldn't run the new aat to check: %v)\n", err)
		return
	}
	fmt.Fprintln(out, "Run aat install-skills to bring them up to date.")
}

// packageManager names the tool that owns an install, and the command that
// updates it, or "" when aat owns it.
func packageManager(path, gopath, home string) (string, string) {
	p := filepath.ToSlash(path)
	if strings.HasPrefix(p, "/opt/homebrew/") || strings.Contains(p, "/Cellar/") || strings.Contains(p, "/linuxbrew/") {
		return "Homebrew", "brew upgrade aat"
	}
	goBin := ""
	switch {
	case gopath != "":
		goBin = filepath.ToSlash(filepath.Join(gopath, "bin"))
	case home != "":
		goBin = filepath.ToSlash(filepath.Join(home, "go", "bin"))
	}
	if goBin != "" && strings.HasPrefix(p, goBin+"/") {
		return "go install", "go install github.com/ohnotnow/agent-acceptance-tracker/cmd/aat@latest"
	}
	return "", ""
}

func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".aat-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// assetName is the release file for a platform. Keep it in step with the
// build matrix in .github/workflows/release.yml.
func assetName(goos, arch string) string {
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (arch != "amd64" && arch != "arm64") {
		return ""
	}
	name := "aat-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func findAsset(assets []ghAsset, name string) (ghAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

func download(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// checksumFor finds name's digest in a SHA256SUMS file ("<hex>  <name>").
func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0], nil
		}
	}
	return "", notFoundErr("SHA256SUMS has no entry for %s", name)
}

// swapIn writes content next to target and renames it over target, so the
// binary is never half-written. Windows can't replace a running .exe, so
// it is moved aside to <target>.old first.
func swapIn(target string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".new-*")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(target + ".old")
		if err := os.Rename(target, target+".old"); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return err
	}
	done = true
	return nil
}
