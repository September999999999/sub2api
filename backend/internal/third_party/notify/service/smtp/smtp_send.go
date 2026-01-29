//go:build !unit

package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	netsmtp "net/smtp"
	"strconv"
)

func sendMail(ctx context.Context, cfg Config, envelopeFrom string, envelopeTo []string, raw []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: timeout}

	var (
		conn net.Conn
		err  error
	)

	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.IgnoreTLS,
		MinVersion:         tls.VersionTLS12,
	}

	if cfg.Secure {
		tcpConn, dialErr := dialer.DialContext(dialCtx, "tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("dial tls tcp: %w", dialErr)
		}

		tlsConn := tls.Client(tcpConn, tlsConfig)
		if deadline, ok := dialCtx.Deadline(); ok {
			_ = tlsConn.SetDeadline(deadline)
		}
		if handshakeErr := tlsConn.HandshakeContext(dialCtx); handshakeErr != nil {
			_ = tcpConn.Close()
			return fmt.Errorf("tls handshake: %w", handshakeErr)
		}
		conn = tlsConn
	} else {
		conn, err = dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial tcp: %w", err)
		}
		if deadline, ok := dialCtx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
	}
	defer func() { _ = conn.Close() }()

	client, err := netsmtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("new smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if !cfg.Secure {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	auth := netsmtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := client.Mail(envelopeFrom); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range envelopeTo {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("rcpt to %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}

	// 邮件在 Data 结束后已发送成功，部分服务器的 QUIT 响应不标准。
	_ = client.Quit()
	return nil
}
