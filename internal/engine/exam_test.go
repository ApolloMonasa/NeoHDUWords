package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

// examMock 模拟考试所需的四个平台端点，记录 save 请求与建卷次数。
type examMock struct {
	mu        sync.Mutex
	t         *testing.T
	questions []sklclient.Question
	answers   map[string]string // title -> 官方正确答案（A/B/C/D）
	newCount  int
	failSave  map[string]bool
	saves     []saveRecord
}

func (m *examMock) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{{PaperID: fmt.Sprintf("p%d", m.newCount)}})
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "1" {
			m.t.Errorf("exam paper type must be 1, got %q", r.URL.Query().Get("type"))
		}
		m.mu.Lock()
		m.newCount++
		id := fmt.Sprintf("p%d", m.newCount)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{PaperID: id, Week: 1, List: m.questions})
	})
	mux.HandleFunc("/api/paper/detail", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("paperId")
		m.mu.Lock()
		defer m.mu.Unlock()
		list := make([]sklclient.Question, 0, len(m.questions))
		for _, q := range m.questions {
			qc := q
			if a, ok := m.answers[q.Title]; ok {
				qc.Answer = a
			}
			list = append(list, qc)
		}
		end := time.Now()
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{PaperID: id, Mark: 100, EndTime: &end, List: list})
	})
	mux.HandleFunc("/api/paper/save", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var rec saveRecord
		_ = json.Unmarshal(b, &rec)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.failSave != nil && m.failSave[rec.PaperID] && len(rec.List) > 0 {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "forbidden"})
			return
		}
		m.saves = append(m.saves, rec)
	})
	return mux
}

func runExamAgainst(t *testing.T, m *examMock, st *store.Store, targetScore int, dryRun bool) error {
	t.Helper()
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)
	cl, err := sklclient.NewFromTokenURL(srv.URL+"/?token=t", sklclient.Options{MaxRPS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return RunExam(context.Background(), ExamOptions{
		Client:           cl,
		Store:            st,
		WaitBeforeSubmit: 0,
		TargetScore:      targetScore,
		DryRun:           dryRun,
		Retry:            SubmitRetryConfig{MaxRetries: 0}.Normalized(),
		Log:              testLogger(t),
	})
}

func knownQuestion(title string) sklclient.Question {
	return sklclient.Question{PaperDetailID: "d-" + title, Title: title, AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d"}
}

func TestRunExam_FullMarks(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for _, title := range []string{"q1", "q2"} {
		if _, _, err := st.UpsertAnswer(ctx, title, []string{"a", "b", "c", "d"}, "a", "seed"); err != nil {
			t.Fatal(err)
		}
	}

	m := &examMock{
		t:         t,
		questions: []sklclient.Question{knownQuestion("q1"), knownQuestion("q2")},
		answers:   map[string]string{"q1": "A", "q2": "A"},
	}
	if err := runExamAgainst(t, m, st, -1, false); err != nil {
		t.Fatal(err)
	}

	if len(m.saves) != 2 {
		t.Fatalf("expected save+submit, got %+v", m.saves)
	}
	save, submit := m.saves[0], m.saves[1]
	if len(save.List) != 2 || save.List[0].Input != "A" || save.List[1].Input != "A" {
		t.Fatalf("expected both correct, got %+v", save.List)
	}
	if len(submit.List) != 0 {
		t.Fatalf("submit must be empty list, got %+v", submit.List)
	}
}

func TestRunExam_TargetScoreHalvesCorrect(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for _, title := range []string{"q1", "q2"} {
		if _, _, err := st.UpsertAnswer(ctx, title, []string{"a", "b", "c", "d"}, "a", "seed"); err != nil {
			t.Fatal(err)
		}
	}

	m := &examMock{
		t:         t,
		questions: []sklclient.Question{knownQuestion("q1"), knownQuestion("q2")},
		answers:   map[string]string{"q1": "A", "q2": "A"},
	}
	if err := runExamAgainst(t, m, st, 50, false); err != nil {
		t.Fatal(err)
	}

	if len(m.saves) != 2 {
		t.Fatalf("expected save+submit, got %+v", m.saves)
	}
	save := m.saves[0]
	if len(save.List) != 2 {
		t.Fatalf("expected 2 answers, got %+v", save.List)
	}
	right, wrong := 0, 0
	for _, q := range save.List {
		if q.Right != nil && *q.Right {
			right++
		} else {
			wrong++
		}
	}
	if right != 1 || wrong != 1 {
		t.Fatalf("expected 1 correct + 1 wrong for 50%%, got right=%d wrong=%d", right, wrong)
	}
}

func TestRunExam_DryRunSubmitsNothing(t *testing.T) {
	st := openTestStore(t)
	m := &examMock{
		t:         t,
		questions: []sklclient.Question{knownQuestion("q1")},
		answers:   map[string]string{"q1": "A"},
	}
	if err := runExamAgainst(t, m, st, -1, true); err != nil {
		t.Fatal(err)
	}
	if len(m.saves) != 0 {
		t.Fatalf("dry-run must not hit save, got %+v", m.saves)
	}
}

func TestRunExam_UnknownQuestionAnsweredRandomly(t *testing.T) {
	st := openTestStore(t)
	m := &examMock{
		t:         t,
		questions: []sklclient.Question{knownQuestion("never-seen")},
		answers:   map[string]string{"never-seen": "C"},
	}
	if err := runExamAgainst(t, m, st, -1, false); err != nil {
		t.Fatal(err)
	}
	if len(m.saves) != 2 {
		t.Fatalf("expected save+submit, got %+v", m.saves)
	}
	q := m.saves[0].List[0]
	if q.Input == "" || q.Right == nil || *q.Right {
		t.Fatalf("expected non-empty random wrong answer, got input=%q right=%v", q.Input, q.Right)
	}
}

func TestRunExam_Save403RecreatesPaper(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, _, err := st.UpsertAnswer(ctx, "q1", []string{"a", "b", "c", "d"}, "a", "seed"); err != nil {
		t.Fatal(err)
	}

	m := &examMock{
		t:         t,
		questions: []sklclient.Question{knownQuestion("q1")},
		answers:   map[string]string{"q1": "A"},
		failSave:  map[string]bool{"p1": true},
	}
	if err := runExamAgainst(t, m, st, -1, false); err != nil {
		t.Fatal(err)
	}

	if m.newCount != 2 {
		t.Fatalf("expected 2 paper creations, got %d", m.newCount)
	}
	if len(m.saves) != 2 || m.saves[0].PaperID != "p2" || m.saves[1].PaperID != "p2" {
		t.Fatalf("expected save+submit on recreated paper p2, got %+v", m.saves)
	}
}
