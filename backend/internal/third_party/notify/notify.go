package notify

import (
	"context"
	"errors"
	"sync"
)

// Message 表示需要发送的通知内容。
type Message struct {
	Subject  string
	Body     string
	Metadata map[string]any
}

func cloneMessage(src *Message) *Message {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Metadata = cloneMetadata(src.Metadata)
	return &dst
}

func cloneMetadata(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneAny(v)
	}
	return dst
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneMetadata(typed)
	case []any:
		if typed == nil {
			return typed
		}
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneAny(typed[i])
		}
		return out
	case []byte:
		if typed == nil {
			return typed
		}
		out := make([]byte, len(typed))
		copy(out, typed)
		return out
	default:
		return v
	}
}

// Notifier 定义渠道发送接口。
type Notifier interface {
	Send(ctx context.Context, msg *Message) error
}

// Notify 聚合多个 Notifier，统一触发发送。
type Notify struct {
	services []Notifier
}

// New 创建一个新的通知聚合器。
func New() *Notify {
	return &Notify{services: make([]Notifier, 0)}
}

// UseServices 注册多个通知渠道。
func (n *Notify) UseServices(notifiers ...Notifier) {
	n.services = append(n.services, notifiers...)
}

// Send 并发发送通知到所有注册渠道，聚合错误返回。
func (n *Notify) Send(ctx context.Context, subject, body string, metadata map[string]any) error {
	msg := &Message{
		Subject:  subject,
		Body:     body,
		Metadata: metadata,
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, svc := range n.services {
		if svc == nil {
			continue
		}
		msgForSvc := cloneMessage(msg)
		wg.Add(1)
		svc := svc
		go func() {
			defer wg.Done()
			if err := svc.Send(ctx, msgForSvc); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}
