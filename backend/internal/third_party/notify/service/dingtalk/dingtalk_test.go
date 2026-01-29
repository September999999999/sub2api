//go:build unit

package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/nikoksr/notify"
)

type capturedRequest struct {
	method      string
	rawQuery    string
	contentType string
	userAgent   string
	query       map[string]string
	body        []byte
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newRecordingClient(statusCode int, responseBody string, capture func(req *http.Request, body []byte)) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			_ = req.Body.Close()
			if capture != nil {
				capture(req, body)
			}

			resp := &http.Response{
				StatusCode: statusCode,
				Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(responseBody)),
				Request:    req,
			}
			resp.Header.Set("Content-Type", "application/json")
			return resp, nil
		}),
	}
}

func TestGenerateDingTalkSign_KnownVector(t *testing.T) {
	const (
		secret         = "secret"
		timestampMS    = int64(1700000000000)
		expectedBase64 = "OuzzJR5+xZ4/EYwqtNt6sMYZQMTa/HEGvc9miJe7XzY="
	)

	got := generateDingTalkSign(secret, timestampMS)
	if got != expectedBase64 {
		t.Fatalf("unexpected sign: got %q want %q", got, expectedBase64)
	}
}

func TestAppendSignParams_InvalidURL(t *testing.T) {
	_, err := appendSignParams("://bad-url", 1, "sign")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestDingTalkNotifier_Send_MarkdownPayload(t *testing.T) {
	var got capturedRequest
	client := newRecordingClient(http.StatusOK, `{"errcode":0,"errmsg":"ok"}`, func(r *http.Request, body []byte) {
		got = capturedRequest{
			method:      r.Method,
			rawQuery:    r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			query: map[string]string{
				"access_token": r.URL.Query().Get("access_token"),
				"timestamp":    r.URL.Query().Get("timestamp"),
				"sign":         r.URL.Query().Get("sign"),
			},
			body: body,
		}
	})

	svc := NewWebhookService("https://oapi.dingtalk.com/robot/send?access_token=token")
	svc.client = client

	err := svc.Send(context.Background(), &notify.Message{
		Subject: "Hello",
		Body:    "World",
	})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}

	if got.method != http.MethodPost {
		t.Fatalf("unexpected method: %s", got.method)
	}
	if !strings.HasPrefix(got.contentType, "application/json") {
		t.Fatalf("unexpected content-type: %q", got.contentType)
	}
	if got.userAgent != "Sub2API-Notifier/1.0" {
		t.Fatalf("unexpected user-agent: %q", got.userAgent)
	}
	if got.query["access_token"] != "token" {
		t.Fatalf("unexpected access_token: %q", got.query["access_token"])
	}
	if got.query["timestamp"] != "" || got.query["sign"] != "" {
		t.Fatalf("expected no sign params, got timestamp=%q sign=%q rawQuery=%q", got.query["timestamp"], got.query["sign"], got.rawQuery)
	}

	var payload dingTalkPayload
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("invalid json payload: %v", err)
	}
	if payload.MsgType != "markdown" {
		t.Fatalf("unexpected msgtype: %q", payload.MsgType)
	}
	if payload.Markdown.Title != "Hello" || payload.Markdown.Text != "World" {
		t.Fatalf("unexpected markdown payload: %+v", payload.Markdown)
	}
}

func TestDingTalkNotifier_Send_WithSign_AppendsQueryParams(t *testing.T) {
	var got capturedRequest

	const (
		timestampMS  = int64(1700000000000)
		secret       = "secret"
		expectedSign = "OuzzJR5+xZ4/EYwqtNt6sMYZQMTa/HEGvc9miJe7XzY="
	)

	client := newRecordingClient(http.StatusOK, `{"errcode":0,"errmsg":"ok"}`, func(r *http.Request, body []byte) {
		got = capturedRequest{
			method:      r.Method,
			rawQuery:    r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			query: map[string]string{
				"access_token": r.URL.Query().Get("access_token"),
				"timestamp":    r.URL.Query().Get("timestamp"),
				"sign":         r.URL.Query().Get("sign"),
			},
			body: body,
		}
	})

	svc := NewWebhookServiceWithSign("https://oapi.dingtalk.com/robot/send?access_token=token", secret)
	svc.client = client
	svc.timestampFunc = func() int64 { return timestampMS }

	err := svc.Send(context.Background(), &notify.Message{Subject: "T", Body: "B"})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}

	if got.query["access_token"] != "token" {
		t.Fatalf("unexpected access_token: %q", got.query["access_token"])
	}
	if got.query["timestamp"] != strconv.FormatInt(timestampMS, 10) {
		t.Fatalf("unexpected timestamp: %q", got.query["timestamp"])
	}
	if got.query["sign"] != expectedSign {
		t.Fatalf("unexpected sign: %q", got.query["sign"])
	}
	if !strings.Contains(got.rawQuery, "sign=OuzzJR5%2BxZ4%2F") {
		t.Fatalf("expected sign to be url-escaped, rawQuery=%q", got.rawQuery)
	}
}

func TestDingTalkNotifier_Send_NilReceiver(t *testing.T) {
	var svc *DingTalkNotifier
	if err := svc.Send(context.Background(), &notify.Message{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDingTalkNotifier_Send_NilMessage(t *testing.T) {
	svc := NewWebhookService("http://example.com")
	if err := svc.Send(context.Background(), nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDingTalkNotifier_Send_EnableSignWithoutSecret(t *testing.T) {
	svc := NewWebhookService("http://example.com")
	svc.enableSign = true
	svc.secret = ""
	svc.timestampFunc = func() int64 { return 1 }

	if err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDingTalkNotifier_Send_Non2xx(t *testing.T) {
	svc := NewWebhookService("https://oapi.dingtalk.com/robot/send?access_token=token")
	svc.client = newRecordingClient(http.StatusInternalServerError, "", nil)
	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestDingTalkNotifier_Send_APIError(t *testing.T) {
	svc := NewWebhookService("https://oapi.dingtalk.com/robot/send?access_token=token")
	svc.client = newRecordingClient(http.StatusOK, `{"errcode":123,"errmsg":"bad"}`, nil)
	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "errcode=123") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDingTalkNotifier_Send_CreateRequestFailure(t *testing.T) {
	svc := NewWebhookService("://bad-url")
	svc.client = newRecordingClient(http.StatusOK, `{"errcode":0,"errmsg":"ok"}`, nil)

	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "create request failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDingTalkNotifier_Send_DoError(t *testing.T) {
	svc := NewWebhookService("https://oapi.dingtalk.com/robot/send?access_token=token")
	svc.client = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		}),
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "send request failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDingTalkNotifier_Send_BuildSignedURLFailure(t *testing.T) {
	svc := &DingTalkNotifier{
		webhookURL:    "://bad-url",
		enableSign:    true,
		secret:        "secret",
		timestampFunc: func() int64 { return 1 },
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "build signed url failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDingTalkNotifier_Send_DefaultClientAndTimestampFuncWhenNil(t *testing.T) {
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	var got capturedRequest
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		got = capturedRequest{
			method:      r.Method,
			rawQuery:    r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			query: map[string]string{
				"access_token": r.URL.Query().Get("access_token"),
				"timestamp":    r.URL.Query().Get("timestamp"),
				"sign":         r.URL.Query().Get("sign"),
			},
			body: body,
		}

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)),
			Request:    r,
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	})

	svc := &DingTalkNotifier{
		webhookURL: "https://oapi.dingtalk.com/robot/send?access_token=token",
		enableSign: true,
		secret:     "secret",
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.method != http.MethodPost {
		t.Fatalf("unexpected method: %s", got.method)
	}
	if got.query["timestamp"] == "" || got.query["sign"] == "" {
		t.Fatalf("expected sign params, got timestamp=%q sign=%q rawQuery=%q", got.query["timestamp"], got.query["sign"], got.rawQuery)
	}

	ts, err := strconv.ParseInt(got.query["timestamp"], 10, 64)
	if err != nil {
		t.Fatalf("invalid timestamp: %v", err)
	}
	expectedSign := generateDingTalkSign("secret", ts)
	if got.query["sign"] != expectedSign {
		t.Fatalf("unexpected sign: got %q want %q", got.query["sign"], expectedSign)
	}
}

func TestNewWebhookServiceWithSign_UsesDefaultTimestampFunc(t *testing.T) {
	var got capturedRequest
	client := newRecordingClient(http.StatusOK, `{"errcode":0,"errmsg":"ok"}`, func(r *http.Request, _ []byte) {
		got = capturedRequest{
			rawQuery: r.URL.RawQuery,
			query: map[string]string{
				"timestamp": r.URL.Query().Get("timestamp"),
				"sign":      r.URL.Query().Get("sign"),
			},
		}
	})

	svc := NewWebhookServiceWithSign("https://oapi.dingtalk.com/robot/send?access_token=token", "secret")
	svc.client = client

	err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.query["timestamp"] == "" || got.query["sign"] == "" {
		t.Fatalf("expected sign params, got timestamp=%q sign=%q rawQuery=%q", got.query["timestamp"], got.query["sign"], got.rawQuery)
	}

	ts, err := strconv.ParseInt(got.query["timestamp"], 10, 64)
	if err != nil {
		t.Fatalf("invalid timestamp: %v", err)
	}
	expectedSign := generateDingTalkSign("secret", ts)
	if got.query["sign"] != expectedSign {
		t.Fatalf("unexpected sign: got %q want %q", got.query["sign"], expectedSign)
	}
}
