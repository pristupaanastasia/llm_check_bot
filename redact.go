package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// redactingClient is an HTTP client for the Telegram API that removes the bot
// token from errors. telegram-bot-api puts the token into the request URL, and
// net/http errors include that URL, so without this the token ends up in logs.
type redactingClient struct {
	http  *http.Client
	token string
}

func newRedactingClient(token string) *redactingClient {
	// Long polling holds a request for up to 30s, so the timeout must exceed it.
	return &redactingClient{http: &http.Client{Timeout: 60 * time.Second}, token: token}
}

func (c *redactingClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New(c.redact(err.Error()))
	}
	return resp, nil
}

func (c *redactingClient) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<TOKEN>")
}
