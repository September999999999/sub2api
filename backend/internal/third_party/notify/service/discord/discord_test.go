//go:build unit

package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nikoksr/notify"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestExtractNotificationType(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "empty", body: "", want: ""},
		{name: "no bracket", body: "hello", want: ""},
		{name: "missing closing", body: "[accountAnomaly hello", want: ""},
		{name: "basic", body: "[accountAnomaly] content\nSeverity: warning", want: "accountAnomaly"},
		{name: "windows newline", body: "[systemError] hi\r\nTime: 2025-01-01T00:00:00Z", want: "systemError"},
		{name: "leading spaces", body: "   [systemError] boom\nx", want: "systemError"},
	}

	for _, tc := range cases {
		if got := extractNotificationType(tc.body); got != tc.want {
			t.Fatalf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestGetDiscordColor(t *testing.T) {
	cases := []struct {
		typ  string
		want int
	}{
		{typ: "accountAnomaly", want: 0xff9800},
		{typ: "systemError", want: 0xf44336},
		{typ: "securityAlert", want: 0xf44336},
		{typ: "rateLimitRecovery", want: 0x4caf50},
		{typ: "test", want: 0x2196f3},
		{typ: "unknown", want: 0x9e9e9e},
		{typ: "", want: 0x9e9e9e},
	}

	for _, tc := range cases {
		if got := getDiscordColor(tc.typ); got != tc.want {
			t.Fatalf("type=%q: want %#x, got %#x", tc.typ, tc.want, got)
		}
	}
}

func TestFormatEmbedFields(t *testing.T) {
	if fields := formatEmbedFields(""); fields != nil {
		t.Fatalf("empty body should return nil fields")
	}

	body := "[accountAnomaly] something happened\r\nSeverity: critical\r\nTime: 2025-01-01T00:00:00Z\r\nMetadata: foo=bar;\r\nNoColonLine"
	want := []embedField{
		{Name: "Content", Value: "something happened"},
		{Name: "Severity", Value: "critical"},
		{Name: "Time", Value: "2025-01-01T00:00:00Z"},
		{Name: "Metadata", Value: "foo=bar;"},
		{Name: "Message", Value: "NoColonLine"},
	}

	got := formatEmbedFields(body)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestDiscordNotifier_Send_Success(t *testing.T) {
	payloadCh := make(chan webhookPayload, 1)
	urlCh := make(chan string, 1)
	methodCh := make(chan string, 1)
	headersCh := make(chan http.Header, 1)

	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			methodCh <- req.Method
			urlCh <- req.URL.String()
			headersCh <- req.Header.Clone()

			body, _ := io.ReadAll(req.Body)
			var got webhookPayload
			if err := json.Unmarshal(body, &got); err != nil {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader("bad json")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			payloadCh <- got

			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	webhookURL := "https://discord.example.com/webhook"
	svc := NewWebhookService(webhookURL)
	svc.client = client
	svc.nowFunc = func() time.Time { return fixed }

	msg := &notify.Message{
		Subject: "Test Title",
		Body:    "[accountAnomaly] something happened\nSeverity: critical\nTime: 2025-01-01T00:00:00Z\nMetadata: foo=bar;",
	}

	if err := svc.Send(context.Background(), msg); err != nil {
		t.Fatalf("send error: %v", err)
	}

	method := <-methodCh
	if method != http.MethodPost {
		t.Fatalf("method want POST, got %s", method)
	}
	gotURL := <-urlCh
	if gotURL != webhookURL {
		t.Fatalf("url want %q, got %q", webhookURL, gotURL)
	}
	headers := <-headersCh
	if ct := headers.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type want application/json, got %q", ct)
	}
	if ua := headers.Get("User-Agent"); ua != defaultUserAgent {
		t.Fatalf("user-agent want %q, got %q", defaultUserAgent, ua)
	}

	got := <-payloadCh
	if got.Username != defaultUsername {
		t.Fatalf("username want %q, got %q", defaultUsername, got.Username)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("embeds want len=1, got %d", len(got.Embeds))
	}

	embed := got.Embeds[0]
	if embed.Title != msg.Subject {
		t.Fatalf("embed title want %q, got %q", msg.Subject, embed.Title)
	}
	if embed.Color != 0xff9800 {
		t.Fatalf("embed color want %#x, got %#x", 0xff9800, embed.Color)
	}
	if embed.Timestamp != fixed.Format(time.RFC3339) {
		t.Fatalf("embed timestamp want %q, got %q", fixed.Format(time.RFC3339), embed.Timestamp)
	}
	if embed.Footer == nil || embed.Footer.Text != defaultUsername {
		t.Fatalf("embed footer want %q, got %#v", defaultUsername, embed.Footer)
	}

	wantFields := []embedField{
		{Name: "Content", Value: "something happened"},
		{Name: "Severity", Value: "critical"},
		{Name: "Time", Value: "2025-01-01T00:00:00Z"},
		{Name: "Metadata", Value: "foo=bar;"},
	}
	if !reflect.DeepEqual(embed.Fields, wantFields) {
		t.Fatalf("embed fields mismatch\nwant: %#v\ngot:  %#v", wantFields, embed.Fields)
	}
}

func TestDiscordNotifier_Send_DefaultColor(t *testing.T) {
	payloadCh := make(chan webhookPayload, 1)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var got webhookPayload
			_ = json.NewDecoder(req.Body).Decode(&got)
			payloadCh <- got
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	svc := NewWebhookService("https://discord.example.com/webhook")
	svc.client = client
	svc.nowFunc = func() time.Time { return time.Unix(0, 0).UTC() }
	msg := &notify.Message{Subject: "t", Body: "no type line"}

	if err := svc.Send(context.Background(), msg); err != nil {
		t.Fatalf("send error: %v", err)
	}

	got := <-payloadCh
	if got.Embeds[0].Color != 0x9e9e9e {
		t.Fatalf("default color want %#x, got %#x", 0x9e9e9e, got.Embeds[0].Color)
	}
}

func TestDiscordNotifier_Send_ErrorCases(t *testing.T) {
	t.Run("nil notifier", func(t *testing.T) {
		var svc *DiscordNotifier
		if err := svc.Send(context.Background(), &notify.Message{}); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("nil message", func(t *testing.T) {
		svc := NewWebhookService("https://example.com")
		if err := svc.Send(context.Background(), nil); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("invalid webhook url", func(t *testing.T) {
		svc := NewWebhookService("://bad-url")
		if err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"}); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("non-2xx response", func(t *testing.T) {
		client := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("boom")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		}

		svc := NewWebhookService("https://discord.example.com/webhook")
		svc.client = client
		svc.nowFunc = nil
		if err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"}); err == nil {
			t.Fatalf("expected error")
		}
	})
}
