//go:build unit

package telegram

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nikoksr/notify"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpResponse(status int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) { return 0, errors.New("boom") }

func TestNormalizeAPIBaseURL(t *testing.T) {
	if got := normalizeAPIBaseURL(""); got != defaultAPIBaseURL {
		t.Fatalf("expected default api base, got %q", got)
	}
	if got := normalizeAPIBaseURL(" https://example.com/ "); got != "https://example.com" {
		t.Fatalf("expected trimmed base, got %q", got)
	}
	if got := normalizeAPIBaseURL("https://example.com////"); got != "https://example.com" {
		t.Fatalf("expected trimmed slashes, got %q", got)
	}
}

func TestFormatText(t *testing.T) {
	msg := &notify.Message{Subject: " Title ", Body: " Body "}
	if got := formatText(msg); got != "Title\nBody" {
		t.Fatalf("unexpected text: %q", got)
	}

	msg = &notify.Message{Subject: "  ", Body: "Body"}
	if got := formatText(msg); got != "Body" {
		t.Fatalf("unexpected text: %q", got)
	}

	msg = &notify.Message{Subject: "Title", Body: "  "}
	if got := formatText(msg); got != "Title" {
		t.Fatalf("unexpected text: %q", got)
	}
}

func TestServiceSend_Success(t *testing.T) {
	token := "token123"
	chatID := "-100123456"

	var got sendMessagePayload

	svc := New(token, chatID)
	svc.apiBaseURL = "https://example.com/"
	svc.client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if r.URL.String() != "https://example.com/bot"+token+"/sendMessage" {
				t.Fatalf("unexpected url: %s", r.URL.String())
			}
			if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("unexpected content-type: %q", ct)
			}
			if ua := r.Header.Get("User-Agent"); ua != defaultUserAgent {
				t.Fatalf("unexpected user-agent: %q", ua)
			}

			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			return httpResponse(http.StatusOK, `{"ok":true}`), nil
		}),
	}

	msg := &notify.Message{Subject: "Title", Body: "Body"}
	if err := svc.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got.ChatID != chatID {
		t.Fatalf("expected chat_id=%q, got %q", chatID, got.ChatID)
	}
	if got.DisableWebPagePreview != true {
		t.Fatalf("expected disable_web_page_preview=true")
	}
	if got.Text != "Title\nBody" {
		t.Fatalf("unexpected text: %q", got.Text)
	}
}

func TestServiceSend_Validation(t *testing.T) {
	svc := New("", "")

	if err := svc.Send(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil message")
	}

	if err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"}); err != ErrMissingBotToken {
		t.Fatalf("expected ErrMissingBotToken, got %v", err)
	}

	svc.botToken = "token"
	if err := svc.Send(context.Background(), &notify.Message{Subject: "t", Body: "b"}); err != ErrMissingChatID {
		t.Fatalf("expected ErrMissingChatID, got %v", err)
	}
}

func TestServiceSend_APIError(t *testing.T) {
	token := "token123"
	chatID := "123"

	svc := New(token, chatID)
	svc.apiBaseURL = "https://example.com"
	svc.client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return httpResponse(http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`), nil
		}),
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "Title", Body: "Body"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected description in error, got %v", err)
	}
}

func TestServiceSend_ReadResponseError(t *testing.T) {
	token := "token123"
	chatID := "123"

	svc := New(token, chatID)
	svc.apiBaseURL = "https://example.com"
	svc.client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(errorReader{}),
			}, nil
		}),
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "Title", Body: "Body"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "read response failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceSend_UnexpectedStatus_NoJSON(t *testing.T) {
	token := "token123"
	chatID := "123"

	svc := New(token, chatID)
	svc.apiBaseURL = "https://example.com"
	svc.client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return httpResponse(http.StatusInternalServerError, "not-json"), nil
		}),
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "Title", Body: "Body"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "unexpected status code: 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceSend_CreateRequestError(t *testing.T) {
	svc := New("token123", "123")
	svc.apiBaseURL = "http://[::1"
	svc.client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return httpResponse(http.StatusOK, `{"ok":true}`), nil
		}),
	}

	err := svc.Send(context.Background(), &notify.Message{Subject: "Title", Body: "Body"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "create request failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateProxyTransport_HTTPAndSOCKS5(t *testing.T) {
	t.Run("http proxy", func(t *testing.T) {
		tr, err := createProxyTransport("http://127.0.0.1:8080")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tr.Proxy == nil {
			t.Fatalf("expected proxy func")
		}
		req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.telegram.org"}}
		u, err := tr.Proxy(req)
		if err != nil {
			t.Fatalf("proxy func error: %v", err)
		}
		if u.String() != "http://127.0.0.1:8080" {
			t.Fatalf("unexpected proxy url: %s", u.String())
		}
	})

	t.Run("socks5 proxy", func(t *testing.T) {
		tr, err := createProxyTransport("socks5://127.0.0.1:1080")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tr.DialContext == nil {
			t.Fatalf("expected dialer")
		}
		if tr.Proxy != nil {
			t.Fatalf("expected http proxy func to be nil for socks5")
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		if _, err := createProxyTransport("ftp://127.0.0.1:21"); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestNewWithSettings_ProxyValidation(t *testing.T) {
	if _, err := NewWithSettings("t", "c", "", "ftp://127.0.0.1:21"); err == nil {
		t.Fatalf("expected unsupported proxy scheme error")
	}
}

func TestNewWithSettings_NoProxy(t *testing.T) {
	svc, err := NewWithSettings("t", "c", "https://example.com///", "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatalf("expected service")
	}
	if svc.apiBaseURL != "https://example.com" {
		t.Fatalf("unexpected apiBaseURL: %q", svc.apiBaseURL)
	}
	if svc.client.Transport != nil {
		t.Fatalf("expected nil transport when proxyUrl is empty")
	}
}

func TestNewWithSettings_HTTPProxy(t *testing.T) {
	svc, err := NewWithSettings("t", "c", "", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := svc.client.Transport.(*http.Transport)
	if !ok || tr == nil || tr.Proxy == nil {
		t.Fatalf("expected http transport with proxy func")
	}
}

func TestCreateProxyTransport_InvalidURL(t *testing.T) {
	if _, err := createProxyTransport("http://[::1"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDialSocks5Proxy_NoAuth(t *testing.T) {
	orig := proxyDialContext
	t.Cleanup(func() { proxyDialContext = orig })

	clientConn, serverConn := net.Pipe()
	proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return clientConn, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- serveSOCKS5Server(serverConn, socks5AuthNone, "", "", "example.com:443", 0x03)
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialSocks5Proxy(ctx, proxyURL, "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestDialSocks5Proxy_UserPass(t *testing.T) {
	orig := proxyDialContext
	t.Cleanup(func() { proxyDialContext = orig })

	clientConn, serverConn := net.Pipe()
	proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return clientConn, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- serveSOCKS5Server(serverConn, socks5AuthUserPass, "user", "pass", "127.0.0.1:80", 0x01)
	}()

	proxyURL, _ := url.Parse("socks5://user:pass@proxy:1080")
	conn, err := dialSocks5Proxy(context.Background(), proxyURL, "tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestDialSocks5Proxy_NonTCP(t *testing.T) {
	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if _, err := dialSocks5Proxy(context.Background(), proxyURL, "udp", "example.com:53"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDialSocks5Proxy_DefaultPort(t *testing.T) {
	orig := proxyDialContext
	t.Cleanup(func() { proxyDialContext = orig })

	clientConn, serverConn := net.Pipe()
	var dialAddr string
	proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialAddr = addr
		return clientConn, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- serveSOCKS5Server(serverConn, socks5AuthNone, "", "", "example.com:443", 0x01)
	}()

	proxyURL, _ := url.Parse("socks5://proxy")
	conn, err := dialSocks5Proxy(context.Background(), proxyURL, "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	if dialAddr != "proxy:1080" {
		t.Fatalf("expected default socks5 port, got %q", dialAddr)
	}
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestDialSocks5Proxy_ProxyHostMissing(t *testing.T) {
	proxyURL, _ := url.Parse("socks5://")
	if _, err := dialSocks5Proxy(context.Background(), proxyURL, "tcp", "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDialSocks5Proxy_DialError(t *testing.T) {
	orig := proxyDialContext
	t.Cleanup(func() { proxyDialContext = orig })
	proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	}

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if _, err := dialSocks5Proxy(context.Background(), proxyURL, "tcp", "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSocks5Handshake_UnsupportedAuthMethod(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			done <- err
			return
		}
		methods := make([]byte, int(greeting[1]))
		if err := readFull(serverConn, methods); err != nil {
			done <- err
			return
		}
		_, err := serverConn.Write([]byte{0x05, 0x99})
		done <- err
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	err := socks5Handshake(clientConn, proxyURL, "example.com:443")
	if err == nil {
		t.Fatalf("expected error")
	}

	_ = <-done
}

func TestSocks5Handshake_UserPassRequiredWithoutCreds(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			done <- err
			return
		}
		methods := make([]byte, int(greeting[1]))
		if err := readFull(serverConn, methods); err != nil {
			done <- err
			return
		}
		_, err := serverConn.Write([]byte{0x05, 0x02})
		done <- err
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	err := socks5Handshake(clientConn, proxyURL, "example.com:443")
	if err == nil {
		t.Fatalf("expected error")
	}

	_ = <-done
}

func TestSocks5Handshake_GreetingWriteError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = serverConn.Close()
	defer clientConn.Close()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSocks5Handshake_ReadMethodError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_ = serverConn.Close()
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5Handshake_InvalidVersionInMethodChoice(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_, _ = serverConn.Write([]byte{0x04, 0x00})
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5Handshake_InvalidDestAddr(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_, _ = serverConn.Write([]byte{0x05, 0x00})
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5Handshake_InvalidDestPort(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_, _ = serverConn.Write([]byte{0x05, 0x00})
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:70000"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5Handshake_ConnectReplyError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_, _ = serverConn.Write([]byte{0x05, 0x00})

		reqHeader := make([]byte, 4)
		if err := readFull(serverConn, reqHeader); err != nil {
			return
		}
		_, _ = readSOCKS5Addr(serverConn, reqHeader[3])
		portBytes := make([]byte, 2)
		_ = readFull(serverConn, portBytes)

		_, _ = serverConn.Write([]byte{0x05, 0x05, 0x00, 0x01})
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5Handshake_DiscardBindAddrError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		greeting := make([]byte, 2)
		if err := readFull(serverConn, greeting); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		_ = readFull(serverConn, methods)
		_, _ = serverConn.Write([]byte{0x05, 0x00})

		reqHeader := make([]byte, 4)
		if err := readFull(serverConn, reqHeader); err != nil {
			return
		}
		_, _ = readSOCKS5Addr(serverConn, reqHeader[3])
		portBytes := make([]byte, 2)
		_ = readFull(serverConn, portBytes)

		_, _ = serverConn.Write([]byte{0x05, 0x00, 0x00, 0x99})
	}()

	proxyURL, _ := url.Parse("socks5://proxy:1080")
	if err := socks5Handshake(clientConn, proxyURL, "example.com:443"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5UserPassAuth_TooLong(t *testing.T) {
	long := strings.Repeat("u", 256)
	if err := socks5UserPassAuth(nil, long, "p"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSocks5UserPassAuth_InvalidVersionAndFailure(t *testing.T) {
	t.Run("invalid version", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		done := make(chan error, 1)
		go func() {
			defer serverConn.Close()
			buf := make([]byte, 5) // 0x01 + ulen + uname + plen + pass
			_ = readFull(serverConn, buf)
			_, err := serverConn.Write([]byte{0x02, 0x00})
			done <- err
		}()

		err := socks5UserPassAuth(clientConn, "u", "p")
		if err == nil {
			t.Fatalf("expected error")
		}
		_ = <-done
	})

	t.Run("auth failed", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		done := make(chan error, 1)
		go func() {
			defer serverConn.Close()
			buf := make([]byte, 5)
			_ = readFull(serverConn, buf)
			_, err := serverConn.Write([]byte{0x01, 0x01})
			done <- err
		}()

		err := socks5UserPassAuth(clientConn, "u", "p")
		if err == nil {
			t.Fatalf("expected error")
		}
		_ = <-done
	})
}

func TestSocks5UserPassAuth_ReadResponseError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 5)
		_ = readFull(serverConn, buf)
		_ = serverConn.Close()
	}()

	if err := socks5UserPassAuth(clientConn, "u", "p"); err == nil {
		t.Fatalf("expected error")
	}
	<-done
}

func TestSocks5UserPassAuth_WriteError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = serverConn.Close()
	defer clientConn.Close()

	if err := socks5UserPassAuth(clientConn, "u", "p"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSocks5AddressBytes_IPv6AndTooLong(t *testing.T) {
	atyp, addr, err := socks5AddressBytes("2001:db8::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atyp != 0x04 || len(addr) != 16 {
		t.Fatalf("unexpected ipv6 encoding: atyp=0x%02x len=%d", atyp, len(addr))
	}

	if _, _, err := socks5AddressBytes(strings.Repeat("a", 256)); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSocks5DiscardBindAddr_IPv6AndInvalid(t *testing.T) {
	t.Run("ipv6", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		done := make(chan error, 1)
		go func() {
			defer serverConn.Close()
			_, err := serverConn.Write(make([]byte, 16+2))
			done <- err
		}()

		if err := socks5DiscardBindAddr(clientConn, 0x04); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = <-done
	})

	t.Run("invalid atyp", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		if err := socks5DiscardBindAddr(clientConn, 0x99); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestSocks5ReplyError_Coverage(t *testing.T) {
	codes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0xff}
	for _, code := range codes {
		if got := socks5ReplyError(code); got == "" {
			t.Fatalf("expected non-empty message for code 0x%02x", code)
		}
	}
	if got := socks5ReplyError(0xff); !strings.Contains(got, "unknown") {
		t.Fatalf("expected unknown message, got %q", got)
	}
}

func TestWriteAll_Error(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = serverConn.Close()
	defer clientConn.Close()

	if err := writeAll(clientConn, []byte("hi")); err == nil {
		t.Fatalf("expected error")
	}
}

const (
	socks5AuthNone     = byte(0x00)
	socks5AuthUserPass = byte(0x02)
)

func serveSOCKS5Server(conn net.Conn, authMethod byte, username, password, expectedDest string, bindAtyp byte) error {
	defer conn.Close()

	// greeting: VER, NMETHODS, METHODS...
	greeting := make([]byte, 2)
	if err := readFull(conn, greeting); err != nil {
		return err
	}
	if greeting[0] != 0x05 {
		return errProtocol("bad version")
	}
	methods := make([]byte, int(greeting[1]))
	if err := readFull(conn, methods); err != nil {
		return err
	}

	if authMethod == socks5AuthUserPass {
		if _, err := conn.Write([]byte{0x05, socks5AuthUserPass}); err != nil {
			return err
		}
		if err := serveSOCKS5UserPassAuth(conn, username, password); err != nil {
			return err
		}
	} else {
		if _, err := conn.Write([]byte{0x05, socks5AuthNone}); err != nil {
			return err
		}
	}

	// request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT
	reqHeader := make([]byte, 4)
	if err := readFull(conn, reqHeader); err != nil {
		return err
	}
	if reqHeader[0] != 0x05 || reqHeader[1] != 0x01 {
		return errProtocol("unsupported cmd")
	}

	destHost, err := readSOCKS5Addr(conn, reqHeader[3])
	if err != nil {
		return err
	}
	portBytes := make([]byte, 2)
	if err := readFull(conn, portBytes); err != nil {
		return err
	}
	destPort := binary.BigEndian.Uint16(portBytes)
	destAddr := net.JoinHostPort(destHost, strconv.Itoa(int(destPort)))
	if destAddr != expectedDest {
		return errProtocol("unexpected dest: " + destAddr)
	}

	// success reply
	if bindAtyp == 0x03 {
		// domain: "abc"
		_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x03, 0x03, 'a', 'b', 'c', 0x00, 0x00})
		return err
	}
	// IPv4: 0.0.0.0:0
	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func serveSOCKS5UserPassAuth(conn net.Conn, username, password string) error {
	// auth request: VER=1, ULEN, UNAME, PLEN, PASS
	h := make([]byte, 2)
	if err := readFull(conn, h); err != nil {
		return err
	}
	if h[0] != 0x01 {
		return errProtocol("bad auth version")
	}
	u := make([]byte, int(h[1]))
	if err := readFull(conn, u); err != nil {
		return err
	}
	pLen := make([]byte, 1)
	if err := readFull(conn, pLen); err != nil {
		return err
	}
	p := make([]byte, int(pLen[0]))
	if err := readFull(conn, p); err != nil {
		return err
	}
	if string(u) != username || string(p) != password {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return errProtocol("bad credentials")
	}
	_, err := conn.Write([]byte{0x01, 0x00})
	return err
}

func readSOCKS5Addr(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		ip := make([]byte, 4)
		if err := readFull(conn, ip); err != nil {
			return "", err
		}
		return net.IP(ip).String(), nil
	case 0x03:
		l := make([]byte, 1)
		if err := readFull(conn, l); err != nil {
			return "", err
		}
		host := make([]byte, int(l[0]))
		if err := readFull(conn, host); err != nil {
			return "", err
		}
		return string(host), nil
	case 0x04:
		ip := make([]byte, 16)
		if err := readFull(conn, ip); err != nil {
			return "", err
		}
		return net.IP(ip).String(), nil
	default:
		return "", errProtocol("bad atyp")
	}
}

type protocolError string

func (e protocolError) Error() string { return string(e) }

func errProtocol(msg string) error { return protocolError(msg) }
