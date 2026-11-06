package aat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Version and RepoURL are stamped by the release workflow with -ldflags.
// A hand-built binary is "dev", or a git description from make install;
// neither is a release, so neither checks for or installs updates.
var (
	Version = "dev"
	RepoURL = "https://github.com/ohnotnow/agent-acceptance-tracker"
)

// isRelease reports whether v is a release version (vMAJOR.MINOR.PATCH).
func isRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// runVersion prints the version, whether the installed skills match the
// ones built into this binary, and, for a release, whether a newer one is out.
func runVersion(w io.Writer, claudeDir, apiURL string) error {
	fmt.Fprintf(w, "aat version %s\n", Version)
	fmt.Fprintln(w, skillsStatus(skillFiles, claudeDir))
	if !isRelease(Version) {
		return nil
	}
	rel, err := fetchLatestRelease(&http.Client{Timeout: 5 * time.Second}, apiURL)
	if err != nil {
		return nil // offline or rate-limited: the version is still worth printing
	}
	if isNewer(rel.TagName, Version) {
		fmt.Fprintf(w, "A newer version (%s) is available: run aat self-update, or see %s/releases/latest\n", rel.TagName, RepoURL)
	} else {
		fmt.Fprintln(w, "This is the latest release.")
	}
	return nil
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

func fetchLatestRelease(client *http.Client, apiURL string) (*ghRelease, error) {
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func latestReleaseURL(repoURL string) string {
	path := strings.TrimSuffix(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
	return "https://api.github.com/repos/" + path + "/releases/latest"
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	if !strings.HasPrefix(v, "v") {
		return out, false
	}
	parts := strings.Split(v[1:], ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// isNewer reports whether latest is a later release than current. Anything
// that isn't vMAJOR.MINOR.PATCH is never newer.
func isNewer(latest, current string) bool {
	l, lok := parseVersion(latest)
	c, cok := parseVersion(current)
	if !lok || !cok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}
