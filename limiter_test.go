package main

import (
	"context"
	"testing"
	"time"

	"github.com/pristupaanastasia/llm_check_bot/internal/config"
	"github.com/pristupaanastasia/llm_check_bot/internal/history"
	"github.com/pristupaanastasia/llm_check_bot/internal/mencount"
)

func TestAcquireModelEnforcesCooldown(t *testing.T) {
	a := &app{cfg: config.Config{MaxLoad: 50}, sem: make(chan struct{}, 1), hist: history.NewMemory(1)}
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

func TestNamesByGender(t *testing.T) {
	e := mencount.Entry{Men: map[string]int{"Дима": 3, "Петя": 1}, Women: map[string]int{"Маша": 2}}
	want := "👨 Дима 3, Петя 1 (всего 4)\n👩 Маша 2 (всего 2)"
	if got := namesByGender(e, 10); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := namesByGender(e, 1); got != "👨 Дима 3, и ещё 1 (всего 4)\n👩 Маша 2 (всего 2)" {
		t.Fatalf("limit: got %q", got)
	}
}
