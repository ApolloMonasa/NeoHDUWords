package store

import (
	"context"
	"fmt"
)

// migrations 是按序执行的建表/变更语句，索引+1 即 schema 版本号（PRAGMA user_version）。
// 新增 schema 变更时只允许追加条目，不得修改历史条目；每条必须是幂等的
// （老库重放无害），因此建表语句一律使用 IF NOT EXISTS。
var migrations = []string{
	// v1: 初始表结构
	`
CREATE TABLE IF NOT EXISTS items_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  stem_raw TEXT NOT NULL,
  options_raw_json TEXT NOT NULL,
  unique_hash TEXT NOT NULL UNIQUE,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS answers_v2 (
  item_id INTEGER PRIMARY KEY,
  correct_text TEXT NOT NULL,
  source TEXT NOT NULL,
  collected_at DATETIME NOT NULL,
  FOREIGN KEY(item_id) REFERENCES items_v2(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS conflicts_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  item_id INTEGER NOT NULL,
  old_correct_text TEXT NOT NULL,
  new_correct_text TEXT NOT NULL,
  observed_at DATETIME NOT NULL,
  source TEXT NOT NULL,
  FOREIGN KEY(item_id) REFERENCES items_v2(id) ON DELETE CASCADE
);
`,
}

func (s *Store) init(ctx context.Context) error {
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
	} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("apply %q: %w", pragma, err)
		}
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version;`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for v := version; v < len(migrations); v++ {
		if _, err := s.db.ExecContext(ctx, migrations[v]); err != nil {
			return fmt.Errorf("apply migration v%d: %w", v+1, err)
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d;`, v+1)); err != nil {
			return fmt.Errorf("set user_version=%d: %w", v+1, err)
		}
	}
	return nil
}
