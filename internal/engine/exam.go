package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

// errRecreatePaper 是哨兵错误：当前试卷 403 失效，需新建试卷后整体重试本轮。
var errRecreatePaper = errors.New("paper expired (403), recreate and retry")

// ExamOptions 是 RunExam 的全部参数。
type ExamOptions struct {
	Client           *sklclient.Client
	Store            *store.Store
	WaitBeforeSubmit time.Duration // 交卷前等待时长；0 表示答完立即交卷
	TargetScore      int           // 目标得分百分比 0-100；<0 表示不控分（尽力全对）
	DryRun           bool          // 只演练不提交
	Retry            SubmitRetryConfig
	Log              LogFunc
}

// RunExam 执行一次完整的正式考试：建卷 → 答题 →（等待）→ 保存 → 交卷 → 回收官方答案。
// 内部对整体流程施加 max(WaitBeforeSubmit+15m, 20m) 的超时。
// 任何阶段检测到凭证失效都会返回带"请重新 login"提示的错误。
func RunExam(ctx context.Context, opts ExamOptions) error {
	err := runExamInner(ctx, opts)
	if sklclient.IsAuthError(err) {
		return fmt.Errorf("登录凭证可能已失效，请重新 login 后再考试: %w", err)
	}
	return err
}

func runExamInner(ctx context.Context, opts ExamOptions) error {
	log := opts.Log
	retryCfg := opts.Retry.Normalized()

	ctx, cancel := context.WithTimeout(ctx, examContextTimeout(opts.WaitBeforeSubmit))
	defer cancel()

	// call 执行一个平台操作；403 按 cfg 重试后仍失败且处于首轮时要求换卷。
	call := func(attempt int, op string, fn func() error) error {
		if err := RetryForbiddenSubmit(ctx, "", op, retryCfg, log, fn); err != nil {
			if attempt == 0 && IsForbiddenAPIError(err) {
				log(LevelWarn, "%s 返回 403，尝试新建试卷重试", op)
				return errRecreatePaper
			}
			return fmt.Errorf("%s: %w", op, err)
		}
		return nil
	}

	var retryPaper *sklclient.Paper
	for attempt := 0; attempt < 2; attempt++ {
		paper, err := nextExamPaper(ctx, opts.Client, retryPaper)
		if err != nil {
			return err
		}
		retryPaper = nil

		err = runExamAttempt(ctx, opts, paper, attempt, call, log)
		if err == nil {
			return nil
		}
		if errors.Is(err, errRecreatePaper) {
			newPaper, nerr := opts.Client.CreateExamPaper(ctx, PaperTypeExam)
			if nerr != nil {
				return fmt.Errorf("%w; CreateExamPaper(retry): %w", err, nerr)
			}
			tmp := newPaper
			retryPaper = &tmp
			continue
		}
		return err
	}
	return fmt.Errorf("执行失败：重试后仍未完成")
}

func examContextTimeout(wait time.Duration) time.Duration {
	timeout := wait + 15*time.Minute
	if timeout < 20*time.Minute {
		timeout = 20 * time.Minute
	}
	return timeout
}

func nextExamPaper(ctx context.Context, cl *sklclient.Client, retryPaper *sklclient.Paper) (sklclient.Paper, error) {
	if retryPaper != nil {
		return *retryPaper, nil
	}
	paper, err := cl.CreateExamPaper(ctx, PaperTypeExam)
	if err != nil {
		return sklclient.Paper{}, fmt.Errorf("CreateExamPaper(exam): %w", err)
	}
	return paper, nil
}

func runExamAttempt(ctx context.Context, opts ExamOptions, paper sklclient.Paper, attempt int, call func(attempt int, op string, fn func() error) error, log LogFunc) error {
	detail, err := opts.Client.PaperDetail(ctx, paper.PaperID)
	if err != nil {
		if attempt == 0 && IsForbiddenAPIError(err) {
			log(LevelWarn, "PaperDetail 返回 403，尝试新建试卷重试")
			return errRecreatePaper
		}
		return fmt.Errorf("PaperDetail: %w", err)
	}

	submission, hit, miss, err := buildExamSubmission(ctx, opts, detail, log)
	if err != nil {
		return err
	}

	log(LevelInfo, "试卷=%s 总题=%d 命中=%d 未命中=%d", paper.PaperID, len(detail.List), hit, miss)

	if opts.DryRun {
		log(LevelInfo, "dry-run 已开启，不提交答案")
		return nil
	}

	if opts.WaitBeforeSubmit > 0 {
		log(LevelInfo, "exam 模式进度条等待 %v 后交卷", opts.WaitBeforeSubmit)
		if err := waitWithProgressBar(ctx, opts.WaitBeforeSubmit, "等待交卷"); err != nil {
			return fmt.Errorf("等待交卷被中断: %w", err)
		}
	}

	if len(submission) > 0 {
		if err := call(attempt, "PaperSave", func() error {
			return opts.Client.PaperSave(ctx, paper.PaperID, submission)
		}); err != nil {
			return err
		}
	}
	if err := call(attempt, "PaperSubmit", func() error {
		return opts.Client.PaperSubmit(ctx, paper.PaperID)
	}); err != nil {
		return err
	}

	res, err := opts.Client.PaperDetail(ctx, paper.PaperID)
	if err != nil {
		return fmt.Errorf("PaperDetail(result): %w", err)
	}

	if ok, listErr := paperInList(ctx, opts.Client, paper.PaperID, PaperTypeExam); listErr != nil {
		log(LevelWarn, "exam 结果校验失败: %v", listErr)
	} else if !ok {
		log(LevelWarn, "exam 试卷未出现在列表中: %s", paper.PaperID)
	}

	added, updated, skipped, err := UpsertCollectedAnswers(ctx, opts.Store, res)
	if err != nil {
		return err
	}
	log(LevelOK, "题目回收: 试卷=%s 入库[新增=%d 更新=%d 跳过=%d]", res.PaperID, added, updated, skipped)

	if res.EndTime != nil {
		log(LevelOK, "提交完成: 得分=%d endTime=%s", res.Mark, res.EndTime.Format(time.RFC3339))
	} else {
		log(LevelOK, "提交完成: 得分=%d", res.Mark)
	}
	return nil
}

// buildExamSubmission 依据题库为整张试卷生成作答：控分时限制答对数量，未知题随机作答。
func buildExamSubmission(ctx context.Context, opts ExamOptions, detail sklclient.PaperDetail, log LogFunc) ([]sklclient.Question, int, int, error) {
	targetCorrect := -1
	correctAssigned := 0
	if opts.TargetScore >= 0 {
		if opts.TargetScore > 100 {
			return nil, 0, 0, fmt.Errorf("--score must be between 0 and 100")
		}
		targetCorrect = (len(detail.List)*opts.TargetScore + 50) / 100
		if targetCorrect > len(detail.List) {
			targetCorrect = len(detail.List)
		}
		log(LevelInfo, "exam 目标得分=%d%%，目标正确题数=%d/%d", opts.TargetScore, targetCorrect, len(detail.List))
	}

	submission := make([]sklclient.Question, 0, len(detail.List))
	hit, miss := 0, 0
	for _, q := range detail.List {
		stem := q.Title
		optList := q.Options()

		var input string
		correctText, ok, err := opts.Store.FindAnswerText(ctx, stem, optList)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("FindAnswerText: %w", err)
		}
		if opts.TargetScore >= 0 {
			useCorrect := ok && correctAssigned < targetCorrect
			if useCorrect {
				idx := indexOf(optList, correctText)
				if idx != -1 {
					hit++
					correctAssigned++
					input = sklclient.IndexToChoice(idx)
					q.Input = input
					t := true
					q.Right = &t
					q.Answer = input
				} else {
					miss++
					input = chooseWrongChoice(correctText, optList)
					q.Input = input
					f := false
					q.Right = &f
				}
			} else {
				miss++
				input = chooseWrongChoice(correctText, optList)
				q.Input = input
				f := false
				q.Right = &f
			}
		} else if ok {
			idx := indexOf(optList, correctText)
			if idx != -1 {
				hit++
				input = sklclient.IndexToChoice(idx)
				q.Input = input
				t := true
				q.Right = &t
				q.Answer = input
			} else {
				miss++
			}
		} else {
			miss++
		}

		// 未知题（题库无匹配）固定随机作答
		if !ok || input == "" {
			if len(optList) > 0 {
				input = sklclient.IndexToChoice(rand.IntN(len(optList)))
			} else {
				input = ""
			}
			q.Input = input
			f := false
			q.Right = &f
		}

		if !opts.DryRun && input != "" {
			submission = append(submission, q)
		}
	}

	if opts.TargetScore >= 0 {
		log(LevelInfo, "exam 评分控制: 已保留正确=%d 目标正确=%d 总题=%d", correctAssigned, targetCorrect, len(detail.List))
	}
	return submission, hit, miss, nil
}

// chooseWrongChoice 从错误选项中随机挑一个，避免控分时答错规律固定。
func chooseWrongChoice(correct string, options []string) string {
	var wrongs []string
	for _, opt := range options {
		if opt != "" && opt != correct {
			wrongs = append(wrongs, opt)
		}
	}
	if len(wrongs) == 0 {
		return ""
	}
	return wrongs[rand.IntN(len(wrongs))]
}

func indexOf(options []string, target string) int {
	for i, opt := range options {
		if opt == target {
			return i
		}
	}
	return -1
}

func paperInList(ctx context.Context, cl *sklclient.Client, paperID string, paperType int) (bool, error) {
	list, err := cl.PaperList(ctx, paperType)
	if err != nil {
		return false, err
	}
	for _, item := range list {
		if item.PaperID == paperID {
			return true, nil
		}
	}
	return false, nil
}

func waitWithProgressBar(ctx context.Context, d time.Duration, label string) error {
	if d <= 0 {
		return nil
	}
	deadline := time.Now().Add(d)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	renderProgressBar(label, d, d)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Println()
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Print("\n")
			return ctx.Err()
		case <-ticker.C:
			elapsed := d - remaining
			renderProgressBar(label, elapsed, d)
		}
	}
}

func renderProgressBar(label string, elapsed, total time.Duration) {
	if total <= 0 {
		total = time.Second
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > total {
		elapsed = total
	}
	const barWidth = 24
	filled := int(float64(barWidth) * float64(elapsed) / float64(total))
	if filled > barWidth {
		filled = barWidth
	}
	percent := int(float64(elapsed) * 100 / float64(total))
	if percent > 100 {
		percent = 100
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	fmt.Printf("\r\x1b[2K%s [%s] %3d%%", label, bar, percent)
}
