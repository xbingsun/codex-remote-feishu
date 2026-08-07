package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/render"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestDeliverUIEventSendsPreviewValidatedFinalImageWithoutCardImageKey(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "formula.png")
	if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	gateway := &messageIDAssigningGateway{}
	app := New(":0", ":0", gateway, agentproto.ServerIdentity{})
	app.SetFinalBlockPreviewer(&stubMarkdownPreviewer{
		text:       " ",
		imagePaths: []string{imagePath},
	})
	app.service.UpsertInstance(&state.InstanceRecord{
		InstanceID:    "inst-1",
		WorkspaceRoot: filepath.Dir(imagePath),
		WorkspaceKey:  filepath.Dir(imagePath),
		Online:        true,
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", CWD: filepath.Dir(imagePath), Loaded: true},
		},
	})
	materializeAttachedSurfaceForFinalCardTest(app, "feishu:app-1:chat:1", "app-1", "chat-1", "ou-user", "inst-1", filepath.Dir(imagePath))

	event := eventcontract.Event{
		Kind:             eventcontract.KindBlockCommitted,
		GatewayID:        "app-1",
		SurfaceSessionID: "feishu:app-1:chat:1",
		SourceMessageID:  "om-source-1",
		Block: &render.Block{
			Kind:       render.BlockAssistantMarkdown,
			InstanceID: "inst-1",
			ThreadID:   "thread-1",
			TurnID:     "turn-1",
			ItemID:     "item-1",
			Text:       "![formula](formula.png)",
			Final:      true,
		},
	}
	if err := app.deliverUIEventWithContext(context.Background(), event); err != nil {
		t.Fatalf("deliver final image: %v", err)
	}

	ops := gateway.snapshotOperations()
	if len(ops) != 1 {
		t.Fatalf("operations = %#v, want one image operation", ops)
	}
	if ops[0].Kind != feishu.OperationSendImage || ops[0].ImagePath != imagePath {
		t.Fatalf("operation = %#v, want validated image send", ops[0])
	}
	if ops[0].ReplyToMessageID != "om-source-1" {
		t.Fatalf("reply target = %q, want om-source-1", ops[0].ReplyToMessageID)
	}
}
