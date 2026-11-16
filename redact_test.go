package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactingClientHidesToken(t *testing.T) {
	const token = "123456:SECRET"
	c := newRedactingClient(token)
	req, _ := http.NewRequest("POST", "http://127.0.0.1:1/bot"+token+"/getMe", nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected connection error")
	}
	if strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "<TOKEN>") {
		t.Fatalf("token not redacted: %v", err)
	}
}
