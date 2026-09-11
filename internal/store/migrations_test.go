package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openRaw(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

func TestOpenMigratesLegacyDB(t *testing.T) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "hduwords-legacy-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	// 构造一个 user_version=0 但已有 _v2 表和数据的“老库”
	raw, err := openRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO items_v2 (stem_raw, options_raw_json, unique_hash, created_at, updated_at)
		VALUES ('legacy-stem', '["a","b","c","d"]', 'legacy-hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO answers_v2 (item_id, correct_text, source, collected_at)
		SELECT id, 'a', 'legacy', '2026-01-01T00:00:00Z' FROM items_v2 WHERE unique_hash='legacy-hash';`); err != nil {
		t.Fatal(err)
	}
	var legacyVersion int
	if err := raw.QueryRow(`PRAGMA user_version;`).Scan(&legacyVersion); err != nil {
		t.Fatal(err)
	}
	if legacyVersion != 0 {
		t.Fatalf("expected legacy user_version=0, got %d", legacyVersion)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	// Open 应迁移到最新版本且数据完好
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var version int
	if err := st.db.QueryRowContext(ctx, `PRAGMA user_version;`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("expected user_version=%d after migration, got %d", len(migrations), version)
	}

	text, ok, err := st.FindAnswerText(ctx, "legacy-stem", []string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatal(err)
	}
	// unique_hash 不匹配（老数据哈希是伪造的），数据仍在但查不到属预期；
	// 直接验证行数没有被迁移破坏
	if ok && text != "a" {
		t.Fatalf("unexpected hit text=%q", text)
	}
	var items, answers int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM items_v2`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM answers_v2`).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	if items != 1 || answers != 1 {
		t.Fatalf("legacy data lost: items=%d answers=%d", items, answers)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	f, err := os.CreateTemp("", "hduwords-idem-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	for i := 0; i < 2; i++ {
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.UpsertAnswer(context.Background(), "stem", []string{"a", "b", "c", "d"}, "a", "test"); err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := st.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Items != 1 || s.Answers != 1 {
		t.Fatalf("expected single row after reopen, got %+v", s)
	}
}

func TestCloseLeavesNoWALFiles(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "clean.db")

	st, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertAnswer(context.Background(), "s", []string{"a", "b", "c", "d"}, "a", "t"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-wal") || strings.HasSuffix(e.Name(), "-shm") {
			t.Fatalf("stale WAL file remains after Close: %s", e.Name())
		}
	}
}
