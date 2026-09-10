package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

// WorkerSpec 一个收集 worker：Tag 用于日志前缀，Client 绑定一个账号。
type WorkerSpec struct {
	Tag    string
	Client *sklclient.Client
}

// BuildWorkers 依据凭证池（优先）或显式 token URL 构造收集 worker 列表。
// workers 为 0 表示自动（每个可用 token 一个），>0 时作为 worker 数上限。
// 某个 token 初始化失败只记录错误并跳过，不影响其他 worker。
func BuildWorkers(poolTokens []string, rawURL string, workers int, opt sklclient.Options, log LogFunc) []WorkerSpec {
	urls := make([]string, 0)
	if len(poolTokens) > 0 {
		for _, tk := range poolTokens {
			urls = append(urls, sklclient.TokenURL(tk))
		}
	} else {
		urls = append(urls, rawURL)
	}

	count := len(urls)
	if workers > 0 && workers < count {
		count = workers
	}
	specs := make([]WorkerSpec, 0, count)
	for i := 0; i < count; i++ {
		tag := fmt.Sprintf("w%02d", i+1)
		cl, err := sklclient.NewFromTokenURL(urls[i], opt)
		if err != nil {
			log(LevelError, "[%s] 初始化客户端失败: %v", tag, err)
			continue
		}
		specs = append(specs, WorkerSpec{Tag: tag, Client: cl})
	}
	return specs
}

// CollectPoolOptions 是 RunCollectPool 的全部参数。
type CollectPoolOptions struct {
	Workers  []WorkerSpec
	Store    *store.Store
	Cooldown time.Duration
	Retry    SubmitRetryConfig
	Log      LogFunc
}

// RunCollectPool 并发运行收集 worker，阻塞直到 ctx 被取消且所有 worker 退出。
func RunCollectPool(ctx context.Context, opts CollectPoolOptions) {
	log := opts.Log
	retryCfg := opts.Retry.Normalized()
	log(LevelInfo, "进入收集模式：workers=%d cooldown=%v submitRetries=%d retryInterval=%v，按 Ctrl+C 退出",
		len(opts.Workers), opts.Cooldown, retryCfg.MaxRetries, retryCfg.Interval)

	var wg sync.WaitGroup
	for _, spec := range opts.Workers {
		wg.Add(1)
		go func(spec WorkerSpec) {
			defer wg.Done()
			runCollectLoop(ctx, spec.Tag, spec.Client, opts.Store, PaperTypePractice, opts.Cooldown, retryCfg, log)
		}(spec)
	}

	workersDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(workersDone)
	}()
	select {
	case <-ctx.Done():
		log(LevelInfo, "收到退出信号，等待 worker 结束")
		<-workersDone
		log(LevelInfo, "收集结束")
	case <-workersDone:
		// 所有 worker 自行退出（如凭证失效），无需等待取消信号
		log(LevelInfo, "全部 worker 已退出，收集结束")
	}
}

func runCollectLoop(ctx context.Context, workerTag string, cl *sklclient.Client, st *store.Store, paperType int, cooldown time.Duration, retryCfg SubmitRetryConfig, log LogFunc) {
	round := 1
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log(LevelRound, "[%s] 第 %d 轮开始", workerTag, round)
		err := runCollectRound(ctx, workerTag, cl, st, paperType, retryCfg, log)
		if err != nil {
			if sklclient.IsAuthError(err) {
				log(LevelError, "[%s] 登录凭证可能已失效，请重新执行 login 后再收集；本 worker 退出（%v）", workerTag, err)
				return
			}
			var apiErr *sklclient.APIError
			waitTime := cooldown
			errText := err.Error()
			shouldDynamicCooldown := strings.Contains(errText, "上次申请时间") || strings.Contains(errText, "短时间重试")
			if errors.As(err, &apiErr) {
				if apiErr.Code == 2 || strings.Contains(apiErr.Msg, "短时间重试") || strings.Contains(apiErr.Msg, "失败") {
					shouldDynamicCooldown = true
				}
			}
			if shouldDynamicCooldown {
				waitTime = calcDynamicCooldown(errText, cooldown)
				log(LevelWarn, "[%s] 频率限制或创建失败，动态冷却=%v，原因=%v", workerTag, waitTime, err)
			} else {
				log(LevelError, "[%s] 本轮失败: %v", workerTag, err)
			}
			log(LevelInfo, "[%s] 冷却等待 %v", workerTag, waitTime)
			if werr := waitWithContext(ctx, waitTime); werr != nil {
				return
			}
		} else {
			log(LevelOK, "[%s] 本轮完成，等待 %v 后进入下一轮", workerTag, cooldown)
			if werr := waitWithContext(ctx, cooldown); werr != nil {
				return
			}
		}
		round++
	}
}

func runCollectRound(ctx context.Context, workerTag string, cl *sklclient.Client, st *store.Store, paperType int, retryCfg SubmitRetryConfig, log LogFunc) error {
	paper, err := cl.CreateFreshPaper(ctx, paperType)
	if err != nil {
		var apiErr *sklclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Code == 2 || strings.Contains(apiErr.Msg, "短时间重试") || strings.Contains(apiErr.Msg, "上次申请时间")) {
			return fmt.Errorf("PaperNew(fresh): %w", err)
		}
		log(LevelWarn, "[%s] 新建试卷失败，回退活跃试卷: %v", workerTag, err)
		paper, err = cl.GetOrCreateActivePaper(ctx, paperType)
		if err != nil {
			return fmt.Errorf("GetOrCreateActivePaper(fallback): %w", err)
		}
	} else {
		log(LevelInfo, "[%s] 已新建试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
	}

	var res sklclient.PaperDetail
	for attempt := 0; attempt < 2; attempt++ {
		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			return fmt.Errorf("PaperDetail(fetch): %w", err)
		}

		submission := make([]sklclient.Question, 0, len(detail.List))
		hit, miss := 0, 0
		// 题库有则答，没有则跳过(提交空)
		for _, q := range detail.List {
			stem := q.Title
			opts := q.Options()

			var input string
			correctText, ok, err := st.FindAnswerText(ctx, stem, opts)
			if err != nil {
				return fmt.Errorf("FindAnswerText: %w", err)
			}

			idx := -1
			if ok {
				for j, opt := range opts {
					if opt == correctText {
						idx = j
						break
					}
				}
			}

			if idx != -1 {
				hit++
				input = sklclient.IndexToChoice(idx)
				q.Input = input
				t := true
				q.Right = &t
				q.Answer = input
			} else {
				miss++
				input = ""
				q.Input = input
				f := false
				q.Right = &f
			}

			if input != "" {
				submission = append(submission, q)
			}
		}

		log(LevelInfo, "[%s] 答题统计: 命中=%d 跳过=%d，准备交卷", workerTag, hit, miss)

		if len(submission) > 0 {
			if err := RetryForbiddenSubmit(ctx, workerTag, "PaperSave", retryCfg, log, func() error {
				return cl.PaperSave(ctx, paper.PaperID, submission)
			}); err != nil {
				if attempt == 0 && IsForbiddenAPIError(err) {
					log(LevelWarn, "[%s] PaperSave 返回 403，当前试卷可能失效，尝试新建试卷重试", workerTag)
					newPaper, nerr := cl.CreateFreshPaper(ctx, paperType)
					if nerr != nil {
						return fmt.Errorf("PaperSave(submit): %w; PaperNew(retry): %w", err, nerr)
					}
					paper = newPaper
					log(LevelInfo, "[%s] 重试改用新试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
					continue
				}
				return fmt.Errorf("PaperSave(submit): %w", err)
			}
		}

		if err := RetryForbiddenSubmit(ctx, workerTag, "PaperSubmit", retryCfg, log, func() error {
			return cl.PaperSubmit(ctx, paper.PaperID)
		}); err != nil {
			if attempt == 0 && IsForbiddenAPIError(err) {
				log(LevelWarn, "[%s] PaperSubmit 返回 403，当前试卷可能失效，尝试新建试卷重试", workerTag)
				newPaper, nerr := cl.CreateFreshPaper(ctx, paperType)
				if nerr != nil {
					return fmt.Errorf("PaperSubmit: %w; PaperNew(retry): %w", err, nerr)
				}
				paper = newPaper
				log(LevelInfo, "[%s] 重试改用新试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
				continue
			}
			return fmt.Errorf("PaperSubmit: %w", err)
		}

		// 提交后重新拉取明细，获取官方正确答案
		res, err = cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			return fmt.Errorf("PaperDetail(result): %w", err)
		}

		break
	}

	added, updated, skipped, err := UpsertCollectedAnswers(ctx, st, res)
	if err != nil {
		return err
	}

	log(LevelOK, "[%s] 收集结果: 得分=%d 试卷=%s 入库[新增=%d 更新=%d 跳过=%d]",
		workerTag, res.Mark, res.PaperID, added, updated, skipped)
	return nil
}
