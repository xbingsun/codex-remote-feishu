package orchestrator

import (
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
)

func TestApplySurfaceActionCarriesMessageIDToNotice(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		ActorUserID:      "ou_actor",
		MessageID:        "drive-comment:docx:docx-token:comment-1:",
		Text:             "帮我处理",
	})
	if len(events) != 1 || events[0].Notice == nil {
		t.Fatalf("expected one notice event, got %#v", events)
	}
	if events[0].SourceMessageID != "drive-comment:docx:docx-token:comment-1:" || events[0].Meta.SourceMessageID != "drive-comment:docx:docx-token:comment-1:" {
		t.Fatalf("expected notice to inherit source message id, got %#v", events[0])
	}
}
