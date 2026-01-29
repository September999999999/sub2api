//go:build unit

package smtp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"testing"
	"time"

	"github.com/nikoksr/notify"
	"github.com/stretchr/testify/require"
)

func TestSplitRecipients(t *testing.T) {
	got := SplitRecipients("a@example.com, b@example.com ; c@example.com;;  ,a@example.com ")
	require.Equal(t, []string{"a@example.com", "b@example.com", "c@example.com"}, got)
}

func TestServiceSend_BuildsMultipartAlternative(t *testing.T) {
	var (
		capturedCfg  Config
		capturedFrom string
		capturedTo   []string
		capturedRaw  []byte
	)

	sendMailHook = func(_ context.Context, cfg Config, envelopeFrom string, envelopeTo []string, raw []byte) error {
		capturedCfg = cfg
		capturedFrom = envelopeFrom
		capturedTo = append([]string(nil), envelopeTo...)
		capturedRaw = append([]byte(nil), raw...)
		return nil
	}
	defer func() { sendMailHook = nil }()

	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com", "b@example.com"},
	})
	svc.now = func() time.Time {
		return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	}

	err := svc.Send(context.Background(), &notify.Message{
		Subject: "Hello",
		Body:    "Line1\nLine2",
	})
	require.NoError(t, err)

	require.Equal(t, defaultPort, capturedCfg.Port)
	require.Equal(t, defaultTimeout, capturedCfg.Timeout)
	require.Equal(t, "user@example.com", capturedFrom)
	require.Equal(t, []string{"a@example.com", "b@example.com"}, capturedTo)

	parsed, err := mail.ReadMessage(bytes.NewReader(capturedRaw))
	require.NoError(t, err)

	decoder := new(mime.WordDecoder)
	decodedSubject, err := decoder.DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, subjectPrefix+"Hello", decodedSubject)

	contentType := parsed.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", mediaType)

	boundary := params["boundary"]
	require.NotEmpty(t, boundary)

	mr := multipart.NewReader(parsed.Body, boundary)

	part1, err := mr.NextPart()
	require.NoError(t, err)
	require.Contains(t, part1.Header.Get("Content-Type"), "text/plain")
	plainQP, err := io.ReadAll(part1)
	require.NoError(t, err)
	plain, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(plainQP)))
	require.NoError(t, err)
	require.Contains(t, string(plain), "Line1")
	require.Contains(t, string(plain), "Line2")

	part2, err := mr.NextPart()
	require.NoError(t, err)
	require.Contains(t, part2.Header.Get("Content-Type"), "text/html")
	htmlQP, err := io.ReadAll(part2)
	require.NoError(t, err)
	htmlDecoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(htmlQP)))
	require.NoError(t, err)
	htmlText := string(htmlDecoded)
	require.Contains(t, htmlText, "linear-gradient")
	require.Contains(t, htmlText, "Claude Relay Service")
	require.Contains(t, htmlText, "Line1")
	require.Contains(t, htmlText, "Line2")

	_, err = mr.NextPart()
	require.Error(t, err)
}

func TestServiceSend_UsesCustomFrom(t *testing.T) {
	var capturedFrom string

	sendMailHook = func(_ context.Context, _ Config, envelopeFrom string, _ []string, _ []byte) error {
		capturedFrom = envelopeFrom
		return nil
	}
	defer func() { sendMailHook = nil }()

	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		From: "Sender Name <sender@example.com>",
		To:   []string{"a@example.com"},
	})

	require.NoError(t, svc.Send(context.Background(), &notify.Message{Subject: "S", Body: "B"}))
	require.Equal(t, "sender@example.com", capturedFrom)
}

func TestServiceSend_RejectsHeaderInjection(t *testing.T) {
	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com"},
	})

	err := svc.Send(context.Background(), &notify.Message{
		Subject: "ok\r\nBcc: x@example.com",
		Body:    "body",
	})
	require.Error(t, err)
}

func TestServiceSend_UsesSendFuncWhenProvided(t *testing.T) {
	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com"},
	})

	svc.SendFunc = func(_ context.Context, _ *notify.Message) error {
		return errors.New("override")
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "S", Body: "B"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "override")
}

func TestServiceSend_RejectsNilMessage(t *testing.T) {
	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com"},
	})

	require.Error(t, svc.Send(context.Background(), nil))
}

func TestServiceSend_RejectsInvalidConfig(t *testing.T) {
	svc := New(Config{
		Host: "",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com"},
	})

	require.Error(t, svc.Send(context.Background(), &notify.Message{Subject: "S", Body: "B"}))
}

func TestServiceSend_PropagatesSendError(t *testing.T) {
	sendMailHook = func(_ context.Context, _ Config, _ string, _ []string, _ []byte) error {
		return errors.New("boom")
	}
	defer func() { sendMailHook = nil }()

	svc := New(Config{
		Host: "smtp.example.com",
		User: "user@example.com",
		Pass: "pass",
		To:   []string{"a@example.com"},
	})

	err := svc.Send(context.Background(), &notify.Message{Subject: "S", Body: "B"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "send failed")
	require.Contains(t, err.Error(), "boom")
}

func TestNormalizeFrom_InvalidAddress(t *testing.T) {
	_, _, err := normalizeFrom(Config{
		User: "user@example.com",
		From: "not-an-email",
	})
	require.Error(t, err)
}

func TestNormalizeTo_EmptyAndInvalid(t *testing.T) {
	_, _, err := normalizeTo([]string{"   ", ""})
	require.Error(t, err)

	_, _, err = normalizeTo([]string{"not-an-email"})
	require.Error(t, err)
}

func TestFormatHTMLBody_Fallbacks(t *testing.T) {
	htmlBody := formatHTMLBody(" ", " ")
	require.Contains(t, htmlBody, "Notification")
	require.Contains(t, htmlBody, "(empty)")
}

func TestBuildMIMEMessage_ValidatesHeaders(t *testing.T) {
	_, err := buildMIMEMessage(mimeMessage{
		From:    "a@example.com\r\nBcc: x@example.com",
		To:      "b@example.com",
		Subject: "s",
		Date:    time.Now(),
		Text:    "t",
		HTML:    "h",
	})
	require.Error(t, err)
}

func TestSendMailUnit_NoHook(t *testing.T) {
	sendMailHook = nil
	require.NoError(t, sendMail(context.Background(), Config{}, "from@example.com", []string{"to@example.com"}, []byte("x")))
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "missing_host",
			cfg:  Config{Port: 587, User: "u", Pass: "p", To: []string{"a@example.com"}},
		},
		{
			name: "bad_port",
			cfg:  Config{Host: "h", Port: 70000, User: "u", Pass: "p", To: []string{"a@example.com"}},
		},
		{
			name: "missing_user",
			cfg:  Config{Host: "h", Port: 587, Pass: "p", To: []string{"a@example.com"}},
		},
		{
			name: "missing_pass",
			cfg:  Config{Host: "h", Port: 587, User: "u", To: []string{"a@example.com"}},
		},
		{
			name: "missing_to",
			cfg:  Config{Host: "h", Port: 587, User: "u", Pass: "p"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, validateConfig(tt.cfg))
		})
	}
}
