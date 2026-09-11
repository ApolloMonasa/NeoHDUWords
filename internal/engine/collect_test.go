package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

func testLogger(t *testing.T) LogFunc {
	t.Helper()
	return func(level, format string, args ...any) {
		t.Logf("[%s] %s", level, fmt.Sprintf(format, args...))
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newTestClient(t *testing.T, srv *httptest.Server) *sklclient.Client {
	t.Helper()
	cl, err := sklclient.NewFromTokenURL(srv.URL+"/?token=t", sklclient.Options{MaxRPS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

type saveRecord struct {
	PaperID string               `json:"paperId"`
	List    []sklclient.Question `json:"list"`
}

func TestRunCollectRound_UpsertsOfficialAnswers(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 预置一题已知答案
	if _, _, err := st.UpsertAnswer(ctx, "known-stem", []string{"a1", "b1", "c1", "d1"}, "b1", "seed"); err != nil {
		t.Fatal(err)
	}

	questions := []sklclient.Question{
		{PaperDetailID: "d1", Title: "known-stem", AnswerA: "a1", AnswerB: "b1", AnswerC: "c1", AnswerD: "d1"},
		{PaperDetailID: "d2", Title: "unknown-stem", AnswerA: "a2", AnswerB: "b2", AnswerC: "c2", AnswerD: "d2"},
	}

	var mu sync.Mutex
	var saves []string
	var lastSave saveRecord

	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{})
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{PaperID: "p1", Week: 1, List: questions})
	})
	mux.HandleFunc("/api/paper/detail", func(w http.ResponseWriter, r *http.Request) {
		// 交卷后带官方正确答案
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{
			PaperID: "p1",
			Mark:    100,
			List: []sklclient.Question{
				{PaperDetailID: "d1", Title: "known-stem", AnswerA: "a1", AnswerB: "b1", AnswerC: "c1", AnswerD: "d1", Answer: "B", Input: "B"},
				{PaperDetailID: "d2", Title: "unknown-stem", AnswerA: "a2", AnswerB: "b2", AnswerC: "c2", AnswerD: "d2", Answer: "C"},
			},
		})
	})
	mux.HandleFunc("/api/paper/save", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var rec saveRecord
		_ = json.Unmarshal(b, &rec)
		mu.Lock()
		defer mu.Unlock()
		if len(rec.List) == 0 {
			saves = append(saves, rec.PaperID+"#submit")
		} else {
			saves = append(saves, rec.PaperID+"#save")
			lastSave = rec
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := newTestClient(t, srv)

	err := runCollectRound(ctx, "w01", cl, st,
		SubmitRetryConfig{MaxRetries: 1, Interval: time.Millisecond}.Normalized(), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(saves) != 2 || saves[0] != "p1#save" || saves[1] != "p1#submit" {
		t.Fatalf("unexpected saves: %v", saves)
	}
	// 未知题不提交，只交已知题
	if len(lastSave.List) != 1 || lastSave.List[0].Title != "known-stem" || lastSave.List[0].Input != "B" {
		t.Fatalf("unexpected save body: %+v", lastSave.List)
	}
	// 官方答案回收入库
	if text, ok, err := st.FindAnswerText(ctx, "unknown-stem", []string{"a2", "b2", "c2", "d2"}); err != nil || !ok || text != "c2" {
		t.Fatalf("expected recycled answer c2, got text=%q ok=%v err=%v", text, ok, err)
	}
}

func TestRunCollectRound_Save403RecreatesPaper(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 预置已知答案，确保 PaperSave 会被调用
	if _, _, err := st.UpsertAnswer(ctx, "q1", []string{"a", "b", "c", "d"}, "a", "seed"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	newCount := 0
	var saves []string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{})
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		newCount++
		id := fmt.Sprintf("p%d", newCount)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{
			PaperID: id,
			Week:    1,
			List:    []sklclient.Question{{PaperDetailID: "d1", Title: "q1", AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d"}},
		})
	})
	mux.HandleFunc("/api/paper/detail", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("paperId")
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{
			PaperID: id,
			List: []sklclient.Question{
				{PaperDetailID: "d1", Title: "q1", AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d", Answer: "A", Input: "A"},
			},
		})
	})
	mux.HandleFunc("/api/paper/save", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var rec saveRecord
		_ = json.Unmarshal(b, &rec)
		if rec.PaperID == "p1" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "forbidden"})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(rec.List) == 0 {
			saves = append(saves, rec.PaperID+"#submit")
		} else {
			saves = append(saves, rec.PaperID+"#save")
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := newTestClient(t, srv)

	err := runCollectRound(ctx, "w01", cl, st,
		SubmitRetryConfig{MaxRetries: 0}.Normalized(), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(saves) != 2 || saves[0] != "p2#save" || saves[1] != "p2#submit" {
		t.Fatalf("expected save/submit on recreated paper p2, got %v", saves)
	}
	if newCount != 2 {
		t.Fatalf("expected 2 paper creations, got %d", newCount)
	}
}

func TestRunCollectPool_ReturnsOnContextCancel(t *testing.T) {
	st := openTestStore(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{})
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "boom"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := newTestClient(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	RunCollectPool(ctx, CollectPoolOptions{
		Workers:  []WorkerSpec{{Tag: "w01", Client: cl}},
		Store:    st,
		Cooldown: 10 * time.Millisecond,
		Retry:    SubmitRetryConfig{MaxRetries: 1, Interval: time.Millisecond},
		Log:      testLogger(t),
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("RunCollectPool did not return promptly after cancel: %v", elapsed)
	}
}

func TestRunCollectPool_WorkerExitsOnAuthError(t *testing.T) {
	st := openTestStore(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{})
	})
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "token已失效，请重新登录"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := newTestClient(t, srv)

	// ctx 永不取消：worker 识别凭证失效自行退出后，RunCollectPool 也必须返回
	start := time.Now()
	RunCollectPool(context.Background(), CollectPoolOptions{
		Workers:  []WorkerSpec{{Tag: "w01", Client: cl}},
		Store:    st,
		Cooldown: 10 * time.Millisecond,
		Retry:    SubmitRetryConfig{MaxRetries: 1, Interval: time.Millisecond},
		Log:      testLogger(t),
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("RunCollectPool did not return after auth failure: %v", elapsed)
	}
}

func TestRunCollectRound_FallbackToActivePaper(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, _, err := st.UpsertAnswer(ctx, "q1", []string{"a", "b", "c", "d"}, "a", "seed"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var saves []string

	mux := http.NewServeMux()
	// 新建试卷持续失败（非限频错误），应回退到列表中的活跃试卷
	mux.HandleFunc("/api/paper/new", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "not allowed now"})
	})
	mux.HandleFunc("/api/paper/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]sklclient.PaperSummary{{PaperID: "active-1", Week: 3}})
	})
	mux.HandleFunc("/api/paper/detail", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sklclient.PaperDetail{
			PaperID: "active-1",
			List: []sklclient.Question{
				{PaperDetailID: "d1", Title: "q1", AnswerA: "a", AnswerB: "b", AnswerC: "c", AnswerD: "d", Answer: "A", Input: "A"},
			},
		})
	})
	mux.HandleFunc("/api/paper/save", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var rec saveRecord
		_ = json.Unmarshal(b, &rec)
		mu.Lock()
		defer mu.Unlock()
		if len(rec.List) == 0 {
			saves = append(saves, rec.PaperID+"#submit")
		} else {
			saves = append(saves, rec.PaperID+"#save")
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := newTestClient(t, srv)

	err := runCollectRound(ctx, "w01", cl, st,
		SubmitRetryConfig{MaxRetries: 1, Interval: time.Millisecond}.Normalized(), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(saves) != 2 || saves[0] != "active-1#save" || saves[1] != "active-1#submit" {
		t.Fatalf("expected save/submit on active paper, got %v", saves)
	}
}

func TestBuildWorkers_PoolPriorityAndCap(t *testing.T) {
	log := func(level, format string, args ...any) {}

	// 凭证池优先，workers 上限生效
	specs := BuildWorkers([]string{"ta", "tb", "tc"}, "https://x.example/?token=ignored", 2, sklclient.Options{}, log)
	if len(specs) != 2 || specs[0].Tag != "w01" || specs[1].Tag != "w02" {
		t.Fatalf("expected 2 capped workers w01/w02, got %+v", specs)
	}

	// 池为空时回退到显式 URL，workers=0 表示自动
	specs = BuildWorkers(nil, "https://x.example/?token=t", 0, sklclient.Options{}, log)
	if len(specs) != 1 {
		t.Fatalf("expected 1 worker from explicit url, got %+v", specs)
	}

	// URL 无 token 时该 worker 初始化失败被跳过
	specs = BuildWorkers(nil, "https://x.example/", 0, sklclient.Options{}, log)
	if len(specs) != 0 {
		t.Fatalf("expected 0 workers for invalid url, got %+v", specs)
	}
}
