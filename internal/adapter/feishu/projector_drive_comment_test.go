package feishu

import (
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/render"
)

func TestProjectorDriveCommentFinalBlockRepliesToComment(t *testing.T) {
	source := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
		ReplyID:   "reply-1",
	})
	ops := NewProjector().ProjectEvent("", eventcontract.Event{
		Kind:             eventcontract.KindBlockCommitted,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		SourceMessageID:  source,
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  "处理好了",
			Final: true,
		},
	})
	if len(ops) != 1 {
		t.Fatalf("expected one operation, got %#v", ops)
	}
	op := ops[0]
	if op.Kind != OperationReplyDriveComment {
		t.Fatalf("kind = %s", op.Kind)
	}
	if op.FileToken != "docx-token" || op.FileType != "docx" || op.CommentID != "comment-1" || op.Text != "处理好了" {
		t.Fatalf("unexpected operation: %#v", op)
	}
}

func TestProjectorDriveCommentNoticeRepliesToComment(t *testing.T) {
	source := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
	})
	ops := NewProjector().ProjectEvent("", eventcontract.Event{
		Kind:             eventcontract.KindNotice,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		SourceMessageID:  source,
		Notice:           &control.Notice{Text: "当前还没有选择会话"},
	})
	if len(ops) != 1 {
		t.Fatalf("expected one operation, got %#v", ops)
	}
	op := ops[0]
	if op.Kind != OperationReplyDriveComment {
		t.Fatalf("kind = %s", op.Kind)
	}
	if op.FileToken != "docx-token" || op.FileType != "docx" || op.CommentID != "comment-1" || op.Text != "当前还没有选择会话" {
		t.Fatalf("unexpected operation: %#v", op)
	}
}

func TestProjectorDriveCommentIgnoresNonFinalBlocks(t *testing.T) {
	source := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
	})
	ops := NewProjector().ProjectEvent("", eventcontract.Event{
		Kind:            eventcontract.KindBlockCommitted,
		SourceMessageID: source,
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  "streaming",
			Final: false,
		},
	})
	if len(ops) != 0 {
		t.Fatalf("expected no operations for non-final doc comment block, got %#v", ops)
	}
}

func TestProjectorDriveCommentIgnoresPendingInputReactions(t *testing.T) {
	source := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
	})
	ops := NewProjector().ProjectEvent("chat-1", eventcontract.Event{
		Kind: eventcontract.KindPendingInput,
		PendingInput: &control.PendingInputState{
			SourceMessageID: source,
			QueueOn:         true,
			TypingOn:        true,
			ThumbsDown:      true,
		},
	})
	if len(ops) != 0 {
		t.Fatalf("expected no IM reaction operations for doc comment source, got %#v", ops)
	}
}
