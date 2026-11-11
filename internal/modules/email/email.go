// Package email owns the outbound mail channel (§11.7): 自建 SMTP，
// 经 Transactional Outbox 异步投递——业务只在同一事务里写 email.send 事件，
// Worker 消费后发送；失败可重试，超过上限进入 failed 死信。
package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yejie/voidxotv/internal/platform/config"
)

// Message 是 outbox_events(event_type='email.send') payload 的形状。
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Sender 用自建 SMTP 直发。STARTTLS（587）为主；无凭据时允许内网中继明文。
type Sender struct {
	host     string
	port     int
	username string
	password string
	from     string
}

func NewSender(cfg config.Config) *Sender {
	return &Sender{
		host: cfg.SMTPHost, port: cfg.SMTPPort,
		username: cfg.SMTPUsername, password: cfg.SMTPPassword, from: cfg.SMTPFrom,
	}
}

// Configured 报告 SMTP 是否已配置；未配置时 Runner 会把事件直接送死信。
func (s *Sender) Configured() bool { return s.host != "" && s.from != "" }

func (s *Sender) Send(ctx context.Context, to, subject, body string) error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial smtp: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: smtp hello: %w", err)
	}
	defer client.Close()

	// STARTTLS 优先：服务器声明支持就升级，明文只在无凭据的内网中继场景出现。
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}
	if s.username != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("email: auth: %w", err)
			}
		}
	}
	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("email: rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := writer.Write([]byte(message(s.from, to, subject, body))); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("email: close data: %w", err)
	}
	return client.Quit()
}

// message 组装 RFC 5322 报文；主题按 RFC 2047 编码支持中文。
func message(from, to, subject, body string) string {
	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + mimeWord(subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + body + "\r\n"
}

// Runner 消费 outbox_events 里的 email.send 事件。
type Runner struct {
	db       *pgxpool.Pool
	sender   *Sender
	interval time.Duration
	attempts int
	log      *slog.Logger
}

func NewRunner(db *pgxpool.Pool, sender *Sender, cfg config.Config, log *slog.Logger) *Runner {
	return &Runner{db: db, sender: sender, interval: 5 * time.Second, attempts: cfg.OutboxMaxAttempts, log: log}
}

// Run 周期消费直到 ctx 取消。
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil {
				r.log.Error("email round failed", "error", err)
			}
		}
	}
}

// RunOnce 处理一小批 pending 的 email.send 事件；FOR UPDATE SKIP LOCKED 允许多实例并行。
func (r *Runner) RunOnce(ctx context.Context) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		UPDATE outbox_events SET status = 'processing', attempts = attempts + 1
		WHERE id IN (
		  SELECT id FROM outbox_events
		  WHERE event_type = 'email.send' AND status = 'pending'
		    AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		  ORDER BY id
		  LIMIT 10
		  FOR UPDATE SKIP LOCKED
		)
		RETURNING id, payload`)
	if err != nil {
		return err
	}
	type claimed struct {
		id      int64
		payload []byte
	}
	var batch []claimed
	for rows.Next() {
		var item claimed
		if err := rows.Scan(&item.id, &item.payload); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for _, item := range batch {
		r.deliver(ctx, item.id, item.payload)
	}
	return nil
}

// deliver 发送单封邮件并回写终态；失败按指数退避重试，超过上限进入 failed 死信。
func (r *Runner) deliver(ctx context.Context, eventID int64, payload []byte) {
	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil || msg.To == "" {
		r.finish(ctx, eventID, "failed", "payload 无效或缺少收件人")
		return
	}
	if !r.sender.Configured() {
		// SMTP 未配置：立即死信并留原因，避免事件无限积压（§11.7 死信告警口径）。
		r.finish(ctx, eventID, "failed", "SMTP 未配置（YJ_SMTP_HOST/YJ_SMTP_FROM）")
		return
	}
	if err := r.sender.Send(ctx, msg.To, msg.Subject, msg.Body); err != nil {
		var attempts int
		_ = r.db.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, eventID).Scan(&attempts)
		if attempts >= r.attempts {
			r.finish(ctx, eventID, "failed", err.Error())
			r.log.Error("email dead letter", "event_id", eventID, "error", err)
			return
		}
		backoff := time.Duration(1<<min(attempts, 6)) * time.Minute
		_, _ = r.db.Exec(ctx, `
			UPDATE outbox_events SET status = 'pending', last_error = $2, next_attempt_at = now() + $3
			WHERE id = $1`, eventID, err.Error(), backoff)
		r.log.Warn("email send deferred", "event_id", eventID, "error", err)
		return
	}
	r.finish(ctx, eventID, "done", "")
	r.log.Info("email sent", "event_id", eventID, "to", msg.To)
}

func (r *Runner) finish(ctx context.Context, eventID int64, status, lastError string) {
	_, _ = r.db.Exec(ctx, `
		UPDATE outbox_events SET status = $2, last_error = NULLIF($3, '')
		WHERE id = $1`, eventID, status, lastError)
}

// mimeWord 按 RFC 2047 编码含非 ASCII 的主题。
func mimeWord(subject string) string {
	ascii := true
	for _, r := range subject {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		return subject
	}
	return "=?UTF-8?B?" + base64Encode(subject) + "?="
}

func base64Encode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
