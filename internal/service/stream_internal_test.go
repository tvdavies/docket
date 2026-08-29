package service

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type deadlineBlockingWriter struct {
	header   http.Header
	deadline time.Time
}

func (writer *deadlineBlockingWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = http.Header{}
	}
	return writer.header
}

func (*deadlineBlockingWriter) WriteHeader(int) {}

func (writer *deadlineBlockingWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadline = deadline
	return nil
}

func (writer *deadlineBlockingWriter) Write([]byte) (int, error) {
	if writer.deadline.IsZero() {
		return 0, context.DeadlineExceeded
	}
	timer := time.NewTimer(time.Until(writer.deadline))
	defer timer.Stop()
	<-timer.C
	return 0, context.DeadlineExceeded
}

func (*deadlineBlockingWriter) Flush() {}

func TestWriteSSEBoundsBlockedConsumers(t *testing.T) {
	previous := streamWriteTimeout
	streamWriteTimeout = 20 * time.Millisecond
	t.Cleanup(func() { streamWriteTimeout = previous })
	started := time.Now()
	err := writeSSE(&deadlineBlockingWriter{}, "live", "", livePayload{Kind: "session", Payload: []byte(`{}`), TTLMS: 1000})
	if err == nil {
		t.Fatal("blocked write unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("blocked write was not bounded: %s", elapsed)
	}
}

func TestPrepareLiveNotificationUsesRemainingTTLAndDropsStaleQueueEntries(t *testing.T) {
	now := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	notification := streamNotification{
		kind:        "live",
		data:        livePayload{Kind: "session", Payload: []byte(`{"state":"working"}`), TTLMS: 1000},
		expiresAt:   now.Add(50 * time.Millisecond),
		liveVersion: 2,
	}
	payload, version, ok := prepareLiveNotification(notification, 1, now)
	if !ok || version != 2 || payload.TTLMS != 50 {
		t.Fatalf("prepared live notification = %#v version=%d ok=%v", payload, version, ok)
	}
	if _, _, ok := prepareLiveNotification(notification, 2, now); ok {
		t.Fatal("notification already represented by snapshot was replayed")
	}
	if _, _, ok := prepareLiveNotification(notification, 1, notification.expiresAt); ok {
		t.Fatal("expired queued notification was replayed")
	}
}
