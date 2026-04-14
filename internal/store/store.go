package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"hduwords/internal/match"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db}
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}

func (s *Store) FindAnswerText(ctx context.Context, stem string, options []string) (string, bool, error) {
	h := match.UniqueHash(stem, options)
	const q = `
SELECT a.correct_text
FROM answers_v2 a
JOIN items_v2 i ON i.id = a.item_id
WHERE i.unique_hash = ?
LIMIT 1;
`
	var correctText string
	err := s.db.QueryRowContext(ctx, q, h).Scan(&correctText)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return correctText, true, nil
}

func (s *Store) UpsertAnswer(ctx context.Context, stem string, options []string, correctText string, source string) (added int, updated int, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	h := match.UniqueHash(stem, options)
	optsJSON, err := json.Marshal(options)
	if err != nil {
		return 0, 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()

	const upsertItem = `
INSERT INTO items_v2 (stem_raw, options_raw_json, unique_hash, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(unique_hash) DO UPDATE SET updated_at=excluded.updated_at
RETURNING id;
`
	var itemID int64
	if err := tx.QueryRowContext(ctx, upsertItem, stem, string(optsJSON), h, now, now).Scan(&itemID); err != nil {
		return 0, 0, err
	}

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT correct_text FROM answers_v2 WHERE item_id=?`, itemID).Scan(&existing)
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, err
	}

	if err == sql.ErrNoRows {
		const ins = `INSERT INTO answers_v2 (item_id, correct_text, source, collected_at) VALUES (?, ?, ?, ?)`
		if _, err := tx.ExecContext(ctx, ins, itemID, correctText, source, now); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}

	if existing != correctText {
		const conflict = `
INSERT INTO conflicts_v2 (item_id, old_correct_text, new_correct_text, observed_at, source)
VALUES (?, ?, ?, ?, ?);
`
		if _, err := tx.ExecContext(ctx, conflict, itemID, existing, correctText, now, source); err != nil {
			return 0, 0, err
		}
	}

	const upd = `UPDATE answers_v2 SET correct_text=?, source=?, collected_at=? WHERE item_id=?`
	if _, err := tx.ExecContext(ctx, upd, correctText, source, now, itemID); err != nil {
		return 0, 0, err
	}
	return 0, 1, nil
}

type Stats struct {
	Items     int
	Answers   int
	Conflicts int
}

type ExportItem struct {
	Stem         string   `json:"stem"`
	Options      []string `json:"options"`
	CorrectIndex int      `json:"correct_index"`
}

func (s *Store) Export(ctx context.Context) ([]ExportItem, error) {
	const q = `
SELECT i.stem_raw, i.options_raw_json, a.correct_text
FROM items_v2 i
JOIN answers_v2 a ON i.id = a.item_id
ORDER BY i.id ASC
`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]ExportItem, 0)
	for rows.Next() {
		var (
			stem     string
			optsJSON string
			correct  string
		)
		if err := rows.Scan(&stem, &optsJSON, &correct); err != nil {
			return nil, err
		}
		var opts []string
		if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
			continue // skip broken json
		}
		idx := -1
		for i, opt := range opts {
			if opt == correct {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue // skip when correct option text no longer appears in options
		}
		res = append(res, ExportItem{
			Stem:         stem,
			Options:      opts,
			CorrectIndex: idx,
		})
	}
	return res, rows.Err()
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var out Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM items_v2`).Scan(&out.Items); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM answers_v2`).Scan(&out.Answers); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM conflicts_v2`).Scan(&out.Conflicts); err != nil {
		return Stats{}, err
	}
	return out, nil
}
