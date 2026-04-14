package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"hduwords/internal/sklclient"
)

func TestCalcDynamicCooldown_ParsesLastApplyTime(t *testing.T) {
	defaultCooldown := 5 * time.Minute
	now := time.Now()
	last := now.Add(-4 * time.Minute).Format("15:04:05")
	msg := fmt.Sprintf("申请考试失败,上次申请时间%s,请勿在短时间重试", last)

	got := calcDynamicCooldown(msg, defaultCooldown)
	if got <= 0 {
		t.Fatalf("expected positive wait, got %v", got)
	}
	if got > 2*time.Minute {
		t.Fatalf("expected cooldown near remaining minute, got %v", got)
	}
}

func TestRetryForbiddenSubmit_SucceedsAfterRetries(t *testing.T) {
	ctx := context.Background()
	cfg := submitRetryConfig{MaxRetries: 3, Interval: time.Millisecond}.normalized()

	attempts := 0
	err := retryForbiddenSubmit(ctx, "", "PaperSubmit", cfg, func() error {
		attempts++
		if attempts <= 2 {
			return &sklclient.APIError{StatusCode: 403, Endpoint: "POST /api/paper/save"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected retry success, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryForbiddenSubmit_NonForbiddenNoRetry(t *testing.T) {
	ctx := context.Background()
	cfg := submitRetryConfig{MaxRetries: 3, Interval: time.Millisecond}.normalized()

	attempts := 0
	err := retryForbiddenSubmit(ctx, "", "PaperSave", cfg, func() error {
		attempts++
		return &sklclient.APIError{StatusCode: 400, Endpoint: "POST /api/paper/save"}
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 1 {
		t.Fatalf("expected no retry for non-403, got attempts=%d", attempts)
	}
}
