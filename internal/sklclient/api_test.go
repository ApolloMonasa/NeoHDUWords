package sklclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestClientFlow(t *testing.T) {
	mux := http.NewServeMux()
	assertCompatHeaders := func(r *http.Request) {
		if got := r.Header.Get("X-Auth-Token"); got != "sid-1" {
			t.Fatalf("X-Auth-Token=%q", got)
		}
		if got := r.Header.Get("skl-ticket"); got != "ticket-1" {
			t.Fatalf("skl-ticket=%q", got)
		}
	}

	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t" {
			t.Fatalf("missing token: %q", r.URL.String())
		}
		assertCompatHeaders(r)
		_ = json.NewEncoder(w).Encode([]PaperSummary{})
	})

	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t" {
			t.Fatalf("missing token: %q", r.URL.String())
		}
		assertCompatHeaders(r)
		if r.URL.Query().Get("type") != "0" {
			t.Fatalf("type=%q", r.URL.Query().Get("type"))
		}
		if _, err := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64); err != nil {
			t.Fatalf("invalid startTime: %v", err)
		}
		_ = json.NewEncoder(w).Encode(PaperDetail{
			PaperID: "p1",
			Week:    6,
			List: []Question{
				{PaperDetailID: "d1", Title: "q1", AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d"},
			},
		})
	})

	mux.HandleFunc("/api/paper/detail", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t" {
			t.Fatalf("missing token: %q", r.URL.String())
		}
		assertCompatHeaders(r)
		if r.URL.Query().Get("paperId") != "p1" {
			t.Fatalf("paperId=%q", r.URL.Query().Get("paperId"))
		}
		_ = json.NewEncoder(w).Encode(PaperDetail{
			PaperID: "p1",
			EndTime: ptrTime(time.Now()),
			Mark:    1,
			List: []Question{
				{PaperDetailID: "d1", Title: "q1", AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d", Answer: "B", Input: "B"},
			},
		})
	})

	var gotSave paperSaveReq
	mux.HandleFunc("/api/paper/save", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t" {
			t.Fatalf("missing token: %q", r.URL.String())
		}
		assertCompatHeaders(r)
		if r.Method != "POST" {
			t.Fatalf("method=%s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotSave); err != nil {
			t.Fatalf("bad body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl, err := NewFromTokenURL(srv.URL+"/?token=t&sessionId=sid-1&skl-ticket=ticket-1#/english/list", Options{MaxRPS: 1000})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	paper, err := cl.GetOrCreateActivePaper(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if paper.PaperID != "p1" {
		t.Fatalf("paper=%+v", paper)
	}

	if _, err := cl.PaperDetail(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if err := cl.PaperSave(ctx, "p1", []Question{{PaperDetailID: "d1", Input: "B"}}); err != nil {
		t.Fatal(err)
	}

	if gotSave.PaperID != "p1" || len(gotSave.List) != 1 || gotSave.List[0].PaperDetailID != "d1" || gotSave.List[0].Input != "B" {
		t.Fatalf("got save: %+v", gotSave)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestGetOrCreateActivePaperPrefersLatestUnfinished(t *testing.T) {
	mux := http.NewServeMux()

	now := time.Now()
	older := now.Add(-10 * time.Minute)
	newer := now.Add(-1 * time.Minute)

	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]PaperSummary{
			{PaperID: "old-active", Week: 5, StartTime: &older, EndTime: nil},
			{PaperID: "new-active", Week: 6, StartTime: &newer, EndTime: nil},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl, err := NewFromTokenURL(srv.URL+"/?token=t", Options{MaxRPS: 1000})
	if err != nil {
		t.Fatal(err)
	}

	paper, err := cl.GetOrCreateActivePaper(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if paper.PaperID != "new-active" {
		t.Fatalf("expected newest active paper, got %q", paper.PaperID)
	}
}

func TestCreateExamPaperDoesNotListPapers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("paper/list should not be called in exam flow")
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t" {
			t.Fatalf("missing token: %q", r.URL.String())
		}
		if r.URL.Query().Get("type") != "1" {
			t.Fatalf("type=%q", r.URL.Query().Get("type"))
		}
		if r.URL.Query().Get("week") != "0" {
			t.Fatalf("week=%q", r.URL.Query().Get("week"))
		}
		_ = json.NewEncoder(w).Encode(PaperDetail{PaperID: "exam-1", Week: 1})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl, err := NewFromTokenURL(srv.URL+"/?token=t&sessionId=sid-1&skl-ticket=ticket-1", Options{MaxRPS: 1000})
	if err != nil {
		t.Fatal(err)
	}

	paper, err := cl.CreateExamPaper(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if paper.PaperID != "exam-1" {
		t.Fatalf("paper=%+v", paper)
	}
}
