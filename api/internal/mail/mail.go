// Package mail queues email in the database and sends it over SMTP from the
// worker, so a slow or unavailable mail server never slows down requests.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/webhooks"
)

// Message is an email to send.
type Message struct {
	To      string
	Subject string
	Kind    string // invitation, magic_link, password_reset, test…
	Body    string // plain text
}

// Queue stores an email for the worker to send, inside the caller's
// transaction, so it's only sent if the change it belongs to commits.
func Queue(ctx context.Context, q db.Querier, m Message) (string, error) {
	if _, err := mail.ParseAddress(m.To); err != nil {
		return "", fmt.Errorf("invalid recipient %q", m.To)
	}
	id := ids.New(ids.Email)
	_, err := q.Exec(ctx, `INSERT INTO emails (id, to_address, subject, kind, body_text) VALUES ($1, $2, $3, $4, $5)`,
		id, m.To, m.Subject, m.Kind, m.Body)
	return id, err
}

// GiveUpAfter is how long failed sends are retried.
const GiveUpAfter = 24 * time.Hour

// Sender delivers queued email over SMTP.
type Sender struct {
	Pool *pgxpool.Pool
	Cfg  config.SMTP
	Log  *slog.Logger
	// Send delivers one message; defaults to SMTP. Tests replace it.
	Send func(ctx context.Context, m Message) error
}

func NewSender(pool *pgxpool.Pool, cfg config.SMTP, log *slog.Logger) *Sender {
	s := &Sender{Pool: pool, Cfg: cfg, Log: log}
	s.Send = s.sendSMTP
	return s
}

// Flush sends due emails and returns how many were sent.
func (s *Sender) Flush(ctx context.Context) (int, error) {
	sent := 0
	for {
		batch, err := s.claim(ctx)
		if err != nil || len(batch) == 0 {
			return sent, err
		}
		for _, e := range batch {
			err := s.Send(ctx, e.Message)
			if err := s.finish(ctx, e, err); err != nil {
				return sent, err
			}
			if err == nil {
				sent++
			}
			if s.Cfg.RatePerSecond > 0 {
				time.Sleep(time.Second / time.Duration(s.Cfg.RatePerSecond))
			}
		}
	}
}

type queued struct {
	Message
	ID        string
	Attempts  int
	CreatedAt time.Time
}

func (s *Sender) claim(ctx context.Context) ([]queued, error) {
	var out []queued
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, to_address, subject, kind, coalesce(body_text, ''), attempts, created_at
			FROM emails WHERE status = 'queued' AND next_attempt_at <= now()
			ORDER BY next_attempt_at LIMIT 20 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (queued, error) {
			var q queued
			return q, r.Scan(&q.ID, &q.To, &q.Subject, &q.Kind, &q.Body, &q.Attempts, &q.CreatedAt)
		})
		if err != nil {
			return err
		}
		// Push them back so a parallel worker doesn't pick them up while we send.
		for _, q := range out {
			if _, err := tx.Exec(ctx, `UPDATE emails SET next_attempt_at = now() + interval '5 minutes' WHERE id = $1`, q.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *Sender) finish(ctx context.Context, e queued, sendErr error) error {
	if sendErr == nil {
		_, err := s.Pool.Exec(ctx, `UPDATE emails SET status = 'sent', sent_at = now(), body_text = NULL, attempts = attempts + 1,
			last_error = NULL WHERE id = $1`, e.ID)
		return err
	}
	s.Log.Warn("send email", "id", e.ID, "kind", e.Kind, "err", sendErr)
	if time.Since(e.CreatedAt) > GiveUpAfter {
		_, err := s.Pool.Exec(ctx, `UPDATE emails SET status = 'failed', body_text = NULL, attempts = attempts + 1, last_error = $2
			WHERE id = $1`, e.ID, sendErr.Error())
		return err
	}
	backoff := time.Duration(1<<min(e.Attempts, 7)) * time.Minute // 1, 2, 4 … 128 minutes
	_, err := s.Pool.Exec(ctx, `UPDATE emails SET attempts = attempts + 1, last_error = $2, next_attempt_at = now() + $3::interval
		WHERE id = $1`, e.ID, sendErr.Error(), fmt.Sprintf("%d seconds", int(backoff.Seconds())))
	return err
}

// Build renders a message as RFC 5322 bytes.
func (s *Sender) Build(m Message) ([]byte, error) {
	from, err := mail.ParseAddress(s.Cfg.From)
	if err != nil {
		return nil, fmt.Errorf("SMTP_FROM: %w", err)
	}
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", m.To)
	if s.Cfg.ReplyTo != "" {
		h("Reply-To", s.Cfg.ReplyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+randomHex(16)+"@"+domainOf(from.Address)+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "8bit")
	h("X-PurrOS-Kind", m.Kind)
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	return b.Bytes(), nil
}

func (s *Sender) sendSMTP(ctx context.Context, m Message) error {
	msg, err := s.Build(m)
	if err != nil {
		return err
	}
	from, _ := mail.ParseAddress(s.Cfg.From)
	addr := net.JoinHostPort(s.Cfg.Host, strconv.Itoa(s.Cfg.Port))
	tlsCfg := &tls.Config{ServerName: s.Cfg.Host, InsecureSkipVerify: !s.Cfg.TLSRejectUnauth, MinVersion: tls.VersionTLS12} //nolint:gosec // opt-in for internal servers

	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if s.Cfg.Secure {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, tlsCfg)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if !s.Cfg.Secure {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
		} else if s.Cfg.RequireTLS {
			return errors.New("the SMTP server doesn't support STARTTLS (set SMTP_REQUIRE_TLS=false to allow plain text)")
		}
	}
	if s.Cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Cfg.User, s.Cfg.Password, s.Cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func domainOf(addr string) string {
	if _, d, ok := strings.Cut(addr, "@"); ok {
		return d
	}
	return "purros.local"
}

// Job returns the worker job that sends queued email.
func (s *Sender) Job() webhooks.Job {
	return webhooks.Job{Name: "email", Every: 5 * time.Second, Run: func(ctx context.Context) error {
		_, err := s.Flush(ctx)
		return err
	}}
}
