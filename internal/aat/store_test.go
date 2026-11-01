package aat

import (
	"context"
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
