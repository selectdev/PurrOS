package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/testutil"
)

func TestEmailQueue(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	ctx := context.Background()
	for _, to := range []string{"a@example.com", "b@example.com"} {
		if _, err := mail.Queue(ctx, env.Pool, mail.Message{To: to, Subject: "Hello é", Kind: "test", Body: "line 1\nline 2"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mail.Queue(ctx, env.Pool, mail.Message{To: "not an address", Kind: "test"}); err == nil {
		t.Fatal("bad address accepted")
	}

	s := mail.NewSender(env.Pool, config.SMTP{Host: "smtp.test", From: "Acme <ops@acme.example>", ReplyTo: "hr@acme.example"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	var sent []string
	s.Send = func(_ context.Context, m mail.Message) error {
		if m.To == "b@example.com" {
			return errors.New("mailbox unavailable")
		}
		msg, err := s.Build(m)
		if err != nil {
			return err
		}
		sent = append(sent, string(msg))
		return nil
	}
	n, err := s.Flush(ctx)
	if err != nil || n != 1 {
		t.Fatalf("flush: %d %v", n, err)
	}
	msg := sent[0]
	for _, want := range []string{"From: \"Acme\" <ops@acme.example>", "To: a@example.com", "Reply-To: hr@acme.example",
		"Subject: =?utf-8?q?Hello_=C3=A9?=", "\r\n\r\nline 1\r\nline 2"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
	var status, lastErr string
	var body *string
	if err := env.Pool.QueryRow(ctx, `SELECT status, body_text FROM emails WHERE to_address = 'a@example.com'`).Scan(&status, &body); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || body != nil {
		t.Fatalf("sent email should be marked and its body cleared: %s %v", status, body)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT status, last_error FROM emails WHERE to_address = 'b@example.com'`).Scan(&status, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || lastErr != "mailbox unavailable" {
		t.Fatalf("failed email should be retried later: %s %s", status, lastErr)
	}
	if n, _ := s.Flush(ctx); n != 0 {
		t.Fatal("retry should wait for its backoff")
	}
}
