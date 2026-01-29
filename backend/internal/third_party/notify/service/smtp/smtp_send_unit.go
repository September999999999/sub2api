//go:build unit

package smtp

import "context"

// sendMailHook 仅用于单元测试拦截发送参数。
var sendMailHook func(ctx context.Context, cfg Config, envelopeFrom string, envelopeTo []string, raw []byte) error

func sendMail(ctx context.Context, cfg Config, envelopeFrom string, envelopeTo []string, raw []byte) error {
	if sendMailHook != nil {
		return sendMailHook(ctx, cfg, envelopeFrom, envelopeTo, raw)
	}
	return nil
}

