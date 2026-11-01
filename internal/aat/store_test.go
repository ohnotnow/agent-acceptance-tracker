package aat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsApplyToAFreshDatabase(t *testing.T) {
	s, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	var version int
	if err := s.db.QueryRow("SELECT version FROM schema_version WHERE id = 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != migrations[len(migrations)-1].version {
		t.Fatalf("schema version = %d, want %d", version, migrations[len(migrations)-1].version)
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id, err := newID(8)
		if err != nil {
			t.Fatal(err)
		}
		if !isID(id, 8) {
			t.Fatalf("id %q is not 8 base62 characters", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q in 1000 draws", id)
		}
		seen[id] = true
	}
}

func TestANewerDatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aat.db")
	s, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec("UPDATE schema_version SET version = 99 WHERE id = 1")
	s.Close()
	if s, err := OpenStore(context.Background(), path); err == nil || !strings.Contains(err.Error(), "upgrade aat") {
		if s != nil {
			s.Close()
		}
		t.Fatalf("err = %v, want a refusal asking to upgrade", err)
	}
}

func TestTheStoreWontCloseAReplacedScenario(t *testing.T) {
	dir := newProject(t)
	f := addFeature(t, dir, "Signing up")
	first := addStory(t, dir, f.ID, "Sign-up form")
	var added struct {
		Scenarios []scenarioOut `json:"scenarios"`
	}
	mustRun(t, dir, postcodeRequired, &added, "scenario", "add", "--story", first.ID, "--body", "-")
	later := addStory(t, dir, f.ID, "Make the postcode optional")
	mustRun(t, dir, postcodeOptional, nil, "scenario", "replace", added.Scenarios[0].ID, "--story", later.ID, "--reason", "x", "--body", "-")

	s, err := OpenStore(context.Background(), filepath.Join(dir, ".aat", "aat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.CloseScenario(context.Background(), added.Scenarios[0].ID, TestRef{File: "tests/x.php", Name: "x"}); err == nil {
		t.Fatal("closing a replaced scenario should be refused")
	}
}
