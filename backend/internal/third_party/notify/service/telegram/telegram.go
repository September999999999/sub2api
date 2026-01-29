package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nikoksr/notify"
)

const (
	defaultAPIBaseURL = "https://api.telegram.org"
	defaultTimeout    = 10 * time.Second
	defaultUserAgent  = "Sub2API-Notifier/1.0"
)

var (
	ErrMissingBotToken = errors.New("telegram: missing bot token")
	ErrMissingChatID   = errors.New("telegram: missing chat id")
)

type sendMessagePayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
}

// Service Telegram Bot 推送服务。
type Service struct {
	botToken   string
	chatID     string
	apiBaseURL string
	client     *http.Client
	// SendFunc 用于测试注入
	SendFunc func(ctx context.Context, msg *notify.Message) error
}

// New 创建使用官方 API 地址的 Telegram 服务。
func New(botToken, chatID string) *Service {
	return &Service{
		botToken:   botToken,
		chatID:     chatID,
		apiBaseURL: defaultAPIBaseURL,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// NewWithSettings 创建支持自定义 API 地址与代理的 Telegram 服务。
func NewWithSettings(botToken, chatID, apiBaseURL, proxyURL string) (*Service, error) {
	svc := New(botToken, chatID)
	svc.apiBaseURL = normalizeAPIBaseURL(apiBaseURL)

	if strings.TrimSpace(proxyURL) == "" {
		return svc, nil
	}

	transport, err := createProxyTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	svc.client.Transport = transport
	return svc, nil
}

// Send 发送通知到 Telegram。
func (s *Service) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	if msg == nil {
		return errors.New("telegram: message is nil")
	}
	if strings.TrimSpace(s.botToken) == "" {
		return ErrMissingBotToken
	}
	if strings.TrimSpace(s.chatID) == "" {
		return ErrMissingChatID
	}

	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(s.apiBaseURL, "/"), s.botToken)

	payload := sendMessagePayload{
		ChatID:                s.chatID,
		Text:                  formatText(msg),
		DisableWebPagePreview: true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read response failed: %w", err)
	}

	var apiResp apiResponse
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &apiResp)
	}

	if resp.StatusCode != http.StatusOK || !apiResp.OK {
		if apiResp.Description != "" || apiResp.ErrorCode != 0 {
			return fmt.Errorf("telegram: api error (status=%d, code=%d): %s", resp.StatusCode, apiResp.ErrorCode, apiResp.Description)
		}
		return fmt.Errorf("telegram: unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func formatText(msg *notify.Message) string {
	parts := make([]string, 0, 2)
	if v := strings.TrimSpace(msg.Subject); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(msg.Body); v != "" {
		parts = append(parts, v)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func normalizeAPIBaseURL(apiBaseURL string) string {
	apiBaseURL = strings.TrimSpace(apiBaseURL)
	if apiBaseURL == "" {
		return defaultAPIBaseURL
	}
	return strings.TrimRight(apiBaseURL, "/")
}

func createProxyTransport(proxyURL string) (*http.Transport, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return nil, fmt.Errorf("telegram: invalid proxy url: %w", err)
	}

	transport := &http.Transport{}

	switch parsedURL.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsedURL)
	case "socks5", "socks5h":
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSocks5Proxy(ctx, parsedURL, network, addr)
		}
	default:
		return nil, fmt.Errorf("telegram: unsupported proxy protocol: %s", parsedURL.Scheme)
	}

	return transport, nil
}

func dialSocks5Proxy(ctx context.Context, proxyURL *url.URL, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("telegram: socks5 only supports tcp, got: %s", network)
	}

	host := proxyURL.Hostname()
	if strings.TrimSpace(host) == "" {
		return nil, errors.New("telegram: socks5 proxy host is required")
	}
	port := proxyURL.Port()
	if port == "" {
		port = "1080"
	}
	proxyAddr := net.JoinHostPort(host, port)

	conn, err := proxyDialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("telegram: connect socks5 proxy failed: %w", err)
	}

	deadline := time.Now().Add(defaultTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	if err := socks5Handshake(conn, proxyURL, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

var proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, addr)
}

func socks5Handshake(conn net.Conn, proxyURL *url.URL, destAddr string) error {
	username := ""
	password := ""
	if proxyURL.User != nil {
		username = proxyURL.User.Username()
		password, _ = proxyURL.User.Password()
	}

	methods := []byte{0x00}
	if proxyURL.User != nil {
		methods = append(methods, 0x02)
	}

	// greeting
	if err := writeAll(conn, append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return fmt.Errorf("telegram: socks5 greeting failed: %w", err)
	}

	choice := make([]byte, 2)
	if err := readFull(conn, choice); err != nil {
		return fmt.Errorf("telegram: socks5 read method failed: %w", err)
	}
	if choice[0] != 0x05 {
		return fmt.Errorf("telegram: socks5 invalid version: %d", choice[0])
	}

	switch choice[1] {
	case 0x00:
		// no auth
	case 0x02:
		if proxyURL.User == nil {
			return errors.New("telegram: socks5 proxy requires username/password")
		}
		if err := socks5UserPassAuth(conn, username, password); err != nil {
			return err
		}
	default:
		return fmt.Errorf("telegram: socks5 unsupported auth method: 0x%02x", choice[1])
	}

	host, portStr, err := net.SplitHostPort(destAddr)
	if err != nil {
		return fmt.Errorf("telegram: socks5 invalid destination addr %q: %w", destAddr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("telegram: socks5 invalid destination port %q", portStr)
	}

	atyp, addrBytes, err := socks5AddressBytes(host)
	if err != nil {
		return err
	}

	req := make([]byte, 0, 4+len(addrBytes)+2)
	req = append(req, 0x05, 0x01, 0x00, atyp)
	req = append(req, addrBytes...)

	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	req = append(req, portBytes[:]...)

	if err := writeAll(conn, req); err != nil {
		return fmt.Errorf("telegram: socks5 connect request failed: %w", err)
	}

	// response header
	header := make([]byte, 4)
	if err := readFull(conn, header); err != nil {
		return fmt.Errorf("telegram: socks5 read connect response failed: %w", err)
	}
	if header[0] != 0x05 {
		return fmt.Errorf("telegram: socks5 invalid version: %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("telegram: socks5 connect failed: %s", socks5ReplyError(header[1]))
	}

	if err := socks5DiscardBindAddr(conn, header[3]); err != nil {
		return err
	}

	return nil
}

func socks5UserPassAuth(conn net.Conn, username, password string) error {
	u := []byte(username)
	p := []byte(password)
	if len(u) > 255 || len(p) > 255 {
		return errors.New("telegram: socks5 username/password too long")
	}

	req := make([]byte, 0, 3+len(u)+len(p))
	req = append(req, 0x01, byte(len(u)))
	req = append(req, u...)
	req = append(req, byte(len(p)))
	req = append(req, p...)

	if err := writeAll(conn, req); err != nil {
		return fmt.Errorf("telegram: socks5 auth request failed: %w", err)
	}

	resp := make([]byte, 2)
	if err := readFull(conn, resp); err != nil {
		return fmt.Errorf("telegram: socks5 auth response failed: %w", err)
	}
	if resp[0] != 0x01 {
		return fmt.Errorf("telegram: socks5 auth invalid version: %d", resp[0])
	}
	if resp[1] != 0x00 {
		return errors.New("telegram: socks5 auth failed")
	}
	return nil
}

func socks5AddressBytes(host string) (byte, []byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return 0x01, v4, nil
		}
		if v6 := ip.To16(); v6 != nil {
			return 0x04, v6, nil
		}
	}

	if len(host) > 255 {
		return 0, nil, errors.New("telegram: socks5 destination host too long")
	}
	addr := append([]byte{byte(len(host))}, []byte(host)...)
	return 0x03, addr, nil
}

func socks5DiscardBindAddr(conn net.Conn, atyp byte) error {
	switch atyp {
	case 0x01:
		// IPv4: 4 bytes addr + 2 bytes port
		buf := make([]byte, 4+2)
		return readFull(conn, buf)
	case 0x04:
		// IPv6: 16 bytes addr + 2 bytes port
		buf := make([]byte, 16+2)
		return readFull(conn, buf)
	case 0x03:
		// domain: 1 len + addr + 2 bytes port
		l := make([]byte, 1)
		if err := readFull(conn, l); err != nil {
			return err
		}
		buf := make([]byte, int(l[0])+2)
		return readFull(conn, buf)
	default:
		return fmt.Errorf("telegram: socks5 invalid bind addr type: 0x%02x", atyp)
	}
}

func socks5ReplyError(rep byte) string {
	switch rep {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "ttl expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("unknown error 0x%02x", rep)
	}
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func readFull(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	return err
}
