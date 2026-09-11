// Package engine 承载 collect/exam 的核心业务流程，CLI 与 TUI 共用。
// 前端只负责收集参数与展示，通过 LogFunc 注入日志输出。
package engine

import "time"

// 日志级别，与历史日志格式保持一致。
const (
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
	LevelOK    = "OK"
	LevelRound = "ROUND"
)

// LogFunc 前端注入的日志回调，签名与各前端的 collectLog 一致。
type LogFunc func(level, format string, args ...any)

// 平台试卷类型：collect 用练习卷，exam 用正式卷。
const (
	PaperTypePractice = 0
	PaperTypeExam     = 1
)

// SubmitRetryConfig 控制提交类请求（save/submit）遇到 403 时的重试策略。
type SubmitRetryConfig struct {
	MaxRetries int
	Interval   time.Duration
}

// Normalized 返回修正了非法值后的配置副本。
func (c SubmitRetryConfig) Normalized() SubmitRetryConfig {
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
	return c
}

// orNoopLog 保证返回非空日志函数，防止调用方漏传 Log 时 panic。
func orNoopLog(log LogFunc) LogFunc {
	if log == nil {
		return func(level, format string, args ...any) {}
	}
	return log
}
