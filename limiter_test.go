package main

import (
	"context"
	"testing"
	"time"

	"github.com/pristupaanastasia/llm_check_bot/internal/config"
)

func TestAcquireModelEnforcesCooldown(t *testing.T) {
	a := &app{cfg: config.Config{MaxLoad: 50}, sem: make(chan struct{}, 1)}
	ctx := context.Background()

	release, ok := a.acquireModel(ctx)
	if !ok {
		t.Fatal("first acquire failed")
	}
	time.Sleep(60 * time.Millisecond) // "model work"
	release()

	start := time.Now()
	release2, ok := a.acquireModel(ctx)
	if !ok {
		t.Fatal("second acquire failed")
	}
	release2()
	// At 50% load a 60ms request is followed by ~60ms of cooldown.
	if waited := time.Since(start); waited < 45*time.Millisecond {
		t.Fatalf("second request waited only %v, want ~60ms cooldown", waited)
	}
}
