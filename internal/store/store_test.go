package store

import (
	"context"
	"os"
	"testing"
)

func TestUpsertAndFind(t *testing.T) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "hduwords-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	stem := "wealth . "
	opts := []string{"health . ", "value . ", "wealth . ", "ring . "}

	text, ok, err := st.FindAnswerText(ctx, stem, opts)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected not found, got text=%q", text)
	}

	added, updated, err := st.UpsertAnswer(ctx, stem, opts, opts[2], "test")
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || updated != 0 {
		t.Fatalf("expected added=1 updated=0, got added=%d updated=%d", added, updated)
	}

	text, ok, err = st.FindAnswerText(ctx, stem, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || text != opts[2] {
		t.Fatalf("expected found text=%q, got ok=%v text=%q", opts[2], ok, text)
	}

	added, updated, err = st.UpsertAnswer(ctx, stem, opts, opts[1], "test2")
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || updated != 1 {
		t.Fatalf("expected added=0 updated=1, got added=%d updated=%d", added, updated)
	}

	items, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 exported item, got %d", len(items))
	}
	if items[0].CorrectIndex != 1 {
		t.Fatalf("expected export correct index=1, got %d", items[0].CorrectIndex)
	}

	s, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Items != 1 || s.Answers != 1 || s.Conflicts != 1 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestExportEmptyReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "hduwords-empty-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	items, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(items) != 0 {
		t.Fatalf("expected zero items, got %d", len(items))
	}
}
