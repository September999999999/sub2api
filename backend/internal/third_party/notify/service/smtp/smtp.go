package smtp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/nikoksr/notify"
)

const (
	defaultPort    = 587
	defaultTimeout = 10 * time.Second
	subjectPrefix  = "[Claude Relay Service] "
)

// Config SMTP 配置。
type Config struct {
	Host      string
	Port      int
	User      string
	Pass      string
	From      string
	To        []string
	Secure    bool
	IgnoreTLS bool
	Timeout   time.Duration
}

// Service SMTP 邮件通知服务。
type Service struct {
	cfg Config

	// SendFunc 用于测试注入自定义行为。
	SendFunc func(ctx context.Context, msg *notify.Message) error

	now func() time.Time
}

// New 创建 SMTP 服务实例。
func New(cfg Config) *Service {
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Service{
		cfg: cfg,
		now: time.Now,
	}
}

// SplitRecipients 将逗号/分号分隔的收件人字符串拆分为列表。
func SplitRecipients(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';'
	})

	recipients := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		recipients = append(recipients, part)
	}
	return recipients
}

// Send 发送邮件通知（同时包含 HTML 与纯文本）。
func (s *Service) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	if msg == nil {
		return errors.New("smtp: message is nil")
	}
	if err := validateConfig(s.cfg); err != nil {
		return err
	}

	headerFrom, envelopeFrom, err := normalizeFrom(s.cfg)
	if err != nil {
		return fmt.Errorf("smtp: invalid from: %w", err)
	}
	headerTo, envelopeTo, err := normalizeTo(s.cfg.To)
	if err != nil {
		return fmt.Errorf("smtp: invalid to: %w", err)
	}

	subject := subjectPrefix + msg.Subject
	if err := validateHeaderValue(subject); err != nil {
		return fmt.Errorf("smtp: invalid subject: %w", err)
	}

	textBody := formatTextBody(msg)
	htmlBody := formatHTMLBody(msg.Subject, msg.Body)

	raw, err := buildMIMEMessage(mimeMessage{
		From:    headerFrom,
		To:      headerTo,
		Subject: subject,
		Date:    s.now().UTC(),
		Text:    textBody,
		HTML:    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("smtp: build message failed: %w", err)
	}

	if err := sendMail(ctx, s.cfg, envelopeFrom, envelopeTo, raw); err != nil {
		return fmt.Errorf("smtp: send failed: %w", err)
	}
	return nil
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return errors.New("smtp: host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("smtp: port must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.User) == "" {
		return errors.New("smtp: user is required")
	}
	if strings.TrimSpace(cfg.Pass) == "" {
		return errors.New("smtp: pass is required")
	}
	if len(cfg.To) == 0 {
		return errors.New("smtp: to is required")
	}
	return nil
}

func normalizeFrom(cfg Config) (headerFrom, envelopeFrom string, err error) {
	from := strings.TrimSpace(cfg.From)
	if from == "" {
		from = strings.TrimSpace(cfg.User)
	}
	if from == "" {
		return "", "", errors.New("from is empty")
	}
	if err := validateHeaderValue(from); err != nil {
		return "", "", err
	}

	addr, err := mail.ParseAddress(from)
	if err != nil {
		return "", "", err
	}
	return addr.String(), addr.Address, nil
}

func normalizeTo(rawTo []string) (headerTo string, envelopeTo []string, err error) {
	headerParts := make([]string, 0, len(rawTo))
	envelope := make([]string, 0, len(rawTo))

	for _, item := range rawTo {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if err := validateHeaderValue(item); err != nil {
			return "", nil, err
		}

		addr, err := mail.ParseAddress(item)
		if err != nil {
			return "", nil, err
		}

		headerParts = append(headerParts, addr.String())
		envelope = append(envelope, addr.Address)
	}

	if len(envelope) == 0 {
		return "", nil, errors.New("no recipients")
	}

	return strings.Join(headerParts, ", "), envelope, nil
}

func validateHeaderValue(v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return errors.New("contains newline")
	}
	return nil
}

func formatTextBody(msg *notify.Message) string {
	return strings.TrimSpace(msg.Body)
}

func formatHTMLBody(title, body string) string {
	escapedTitle := html.EscapeString(strings.TrimSpace(title))
	escapedBody := html.EscapeString(strings.TrimSpace(body))

	if escapedTitle == "" {
		escapedTitle = "Notification"
	}
	if escapedBody == "" {
		escapedBody = "(empty)"
	}

	return fmt.Sprintf(emailHTMLTemplate, escapedTitle, escapedTitle, escapedBody)
}

const emailHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>%s</title>
  <style>
    body {
      margin: 0;
      padding: 0;
      background: #f5f7fb;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Arial, "Noto Sans", "Helvetica Neue", sans-serif;
      color: #111827;
    }
    .container {
      max-width: 680px;
      margin: 0 auto;
      padding: 24px 16px;
    }
    .card {
      background: #ffffff;
      border-radius: 14px;
      overflow: hidden;
      box-shadow: 0 10px 30px rgba(17, 24, 39, 0.10);
      border: 1px solid rgba(17, 24, 39, 0.06);
    }
    .header {
      padding: 22px 24px;
      background: linear-gradient(135deg, #667eea, #764ba2);
      color: #ffffff;
    }
    .brand {
      font-size: 13px;
      opacity: 0.9;
      letter-spacing: 0.4px;
      text-transform: uppercase;
    }
    .title {
      margin: 10px 0 0 0;
      font-size: 18px;
      font-weight: 700;
      line-height: 1.3;
      word-break: break-word;
    }
    .content {
      padding: 20px 24px 10px 24px;
    }
    pre {
      margin: 0;
      padding: 14px 14px;
      background: #f3f4f6;
      border-radius: 10px;
      border: 1px solid rgba(17, 24, 39, 0.08);
      white-space: pre-wrap;
      word-break: break-word;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      font-size: 13px;
      line-height: 1.55;
      color: #111827;
    }
    .footer {
      padding: 14px 24px 22px 24px;
      color: #6b7280;
      font-size: 12px;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="card">
      <div class="header">
        <div class="brand">Claude Relay Service</div>
        <div class="title">%s</div>
      </div>
      <div class="content">
        <pre>%s</pre>
      </div>
      <div class="footer">This email is generated automatically by Sub2API.</div>
    </div>
  </div>
</body>
</html>`

type mimeMessage struct {
	From    string
	To      string
	Subject string
	Date    time.Time
	Text    string
	HTML    string
}

func buildMIMEMessage(m mimeMessage) ([]byte, error) {
	if err := validateHeaderValue(m.From); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if err := validateHeaderValue(m.To); err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	if err := validateHeaderValue(m.Subject); err != nil {
		return nil, fmt.Errorf("subject: %w", err)
	}

	boundary := fmt.Sprintf("sub2api_%d", time.Now().UnixNano())

	var buf bytes.Buffer
	writeHeaderLine(&buf, "From", m.From)
	writeHeaderLine(&buf, "To", m.To)
	writeHeaderLine(&buf, "Subject", encodeSubject(m.Subject))
	writeHeaderLine(&buf, "Date", m.Date.Format(time.RFC1123Z))
	writeHeaderLine(&buf, "MIME-Version", "1.0")
	writeHeaderLine(&buf, "Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	buf.WriteString("\r\n")

	if err := writeMIMEPart(&buf, boundary, "text/plain; charset=UTF-8", m.Text); err != nil {
		return nil, err
	}
	if err := writeMIMEPart(&buf, boundary, "text/html; charset=UTF-8", m.HTML); err != nil {
		return nil, err
	}

	buf.WriteString("--")
	buf.WriteString(boundary)
	buf.WriteString("--\r\n")

	return buf.Bytes(), nil
}

func encodeSubject(subject string) string {
	return mime.QEncoding.Encode("UTF-8", subject)
}

func writeHeaderLine(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

func writeMIMEPart(buf *bytes.Buffer, boundary, contentType, content string) error {
	buf.WriteString("--")
	buf.WriteString(boundary)
	buf.WriteString("\r\n")
	writeHeaderLine(buf, "Content-Type", contentType)
	writeHeaderLine(buf, "Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(buf)
	if _, err := qp.Write([]byte(content)); err != nil {
		_ = qp.Close()
		return err
	}
	if err := qp.Close(); err != nil {
		return err
	}
	buf.WriteString("\r\n")
	return nil
}
