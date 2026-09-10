package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

// IsForbiddenAPIError 判断 err 是否为平台 403 错误。
func IsForbiddenAPIError(err error) bool {
	var apiErr *sklclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 403
}

// RetryForbiddenSubmit 执行 fn，遇 403 按 cfg 重试；非 403 错误原样返回。
func RetryForbiddenSubmit(ctx context.Context, workerTag, opName string, cfg SubmitRetryConfig, log LogFunc, fn func() error) error {
	err := fn()
	if err == nil || !IsForbiddenAPIError(err) || cfg.MaxRetries == 0 {
		return err
	}

	for i := 1; i <= cfg.MaxRetries; i++ {
		if workerTag != "" {
			log(LevelWarn, "[%s] %s 返回 403，%v 后进行第 %d/%d 次重试", workerTag, opName, cfg.Interval, i, cfg.MaxRetries)
		} else {
			log(LevelWarn, "%s 返回 403，%v 后进行第 %d/%d 次重试", opName, cfg.Interval, i, cfg.MaxRetries)
		}
		if werr := waitWithContext(ctx, cfg.Interval); werr != nil {
			return werr
		}
		err = fn()
		if err == nil {
			if workerTag != "" {
				log(LevelOK, "[%s] %s 403 重试成功", workerTag, opName)
			} else {
				log(LevelOK, "%s 403 重试成功", opName)
			}
			return nil
		}
		if !IsForbiddenAPIError(err) {
			return err
		}
	}

	return err
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errTimeRegexp = regexp.MustCompile(`上次申请时间(\d{2}:\d{2}:\d{2})`)

// calcDynamicCooldown 从"上次申请时间HH:mm:ss"限频报错中推算剩余冷却时间。
func calcDynamicCooldown(errMsg string, defaultCooldown time.Duration) time.Duration {
	m := errTimeRegexp.FindStringSubmatch(errMsg)
	if len(m) < 2 {
		return defaultCooldown
	}
	timeStr := m[1]

	now := time.Now()
	t, err := time.ParseInLocation("15:04:05", timeStr, now.Location())
	if err != nil {
		return defaultCooldown
	}

	lastReqTime := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	// if lastReqTime is in the future, it might be due to timezone/clock skew, or crossing midnight.
	if lastReqTime.After(now) {
		lastReqTime = lastReqTime.Add(-24 * time.Hour)
	}

	elapsed := now.Sub(lastReqTime)
	if elapsed >= defaultCooldown {
		return 5 * time.Second // 已经超过冷却时间但可能服务器时间不一致，给个最小等待时间
	}
	return defaultCooldown - elapsed + 2*time.Second // 加 2 秒冗余，避免刚卡点请求失败
}

// UpsertCollectedAnswers 把交卷后拉取的试卷明细（含官方正确答案）回写题库。
// 返回新增、更新、跳过的条数。
func UpsertCollectedAnswers(ctx context.Context, st *store.Store, res sklclient.PaperDetail) (int, int, int, error) {
	added, updated, skipped := 0, 0, 0
	for _, q := range res.List {
		answerChoice := q.Answer
		if answerChoice == "" && q.Right != nil && *q.Right {
			answerChoice = q.Input
		}
		if answerChoice == "" {
			skipped++
			continue
		}
		cidx, ok := sklclient.ChoiceToIndex(answerChoice)
		if !ok {
			skipped++
			continue
		}
		a, u, err := st.UpsertAnswer(ctx, q.Title, q.Options(), q.Options()[cidx], "api_detail")
		if err != nil {
			return 0, 0, 0, fmt.Errorf("UpsertAnswer: %w", err)
		}
		added += a
		updated += u
	}
	return added, updated, skipped, nil
}
