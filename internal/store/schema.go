package store

const schemaSQL = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

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
`

