package notify

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type captureMutateNotifier struct {
	id int

	mu   sync.Mutex
	msgs []*Message
}

func (n *captureMutateNotifier) Send(_ context.Context, msg *Message) error {
	n.mu.Lock()
	n.msgs = append(n.msgs, msg)
	n.mu.Unlock()

	if msg.Metadata == nil {
		msg.Metadata = map[string]any{}
	}

	for i := 0; i < 500; i++ {
		msg.Subject = fmt.Sprintf("svc=%d i=%d", n.id, i)
		msg.Body = fmt.Sprintf("body=%d", i)
		msg.Metadata[fmt.Sprintf("svc_%d", n.id)] = i

		nested, ok := msg.Metadata["nested"].(map[string]any)
		if ok {
			nested[fmt.Sprintf("svc_%d", n.id)] = i
		}
	}

	return nil
}

func (n *captureMutateNotifier) Captured() []*Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*Message, len(n.msgs))
	copy(out, n.msgs)
	return out
}

func TestNotify_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	svc1 := &captureMutateNotifier{id: 1}
	svc2 := &captureMutateNotifier{id: 2}

	n := New()
	n.UseServices(svc1, svc2)

	metadata := map[string]any{
		"base": "value",
		"nested": map[string]any{
			"shared": true,
		},
	}

	err := n.Send(context.Background(), "subject", "body", metadata)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	msgs1 := svc1.Captured()
	msgs2 := svc2.Captured()
	if len(msgs1) != 1 || len(msgs2) != 1 {
		t.Fatalf("expected 1 msg per notifier, got %d and %d", len(msgs1), len(msgs2))
	}

	if msgs1[0] == msgs2[0] {
		t.Fatalf("expected distinct Message instances per notifier")
	}

	if msgs1[0].Metadata == nil || msgs2[0].Metadata == nil {
		t.Fatalf("expected metadata not nil")
	}

	if _, ok := msgs1[0].Metadata["svc_2"]; ok {
		t.Fatalf("expected svc_1 message metadata not to include svc_2 key")
	}
	if _, ok := msgs2[0].Metadata["svc_1"]; ok {
		t.Fatalf("expected svc_2 message metadata not to include svc_1 key")
	}

	nested1, ok1 := msgs1[0].Metadata["nested"].(map[string]any)
	nested2, ok2 := msgs2[0].Metadata["nested"].(map[string]any)
	if !ok1 || !ok2 {
		t.Fatalf("expected nested metadata to be map[string]any")
	}
	if _, ok := nested1["svc_2"]; ok {
		t.Fatalf("expected svc_1 nested metadata not to include svc_2 key")
	}
	if _, ok := nested2["svc_1"]; ok {
		t.Fatalf("expected svc_2 nested metadata not to include svc_1 key")
	}
}

