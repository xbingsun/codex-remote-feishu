package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/render"
)

func TestProjectFinalImagePathsUsesImageReplyLane(t *testing.T) {
	projector := NewProjector()
	imagePath := writeProjectorFinalImage(t, filepath.Join(t.TempDir(), "ppo-actor-critic-loss-formula.png"))
	event := eventcontract.Event{
		Kind:             eventcontract.KindBlockCommitted,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		SourceMessageID:  "om-source-1",
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Final: true,
		},
	}
	ops := projector.ProjectFinalImagePaths("chat-1", event, []string{imagePath})
	if len(ops) != 1 || ops[0].Kind != OperationSendImage {
		t.Fatalf("expected resolved final image to use image lane, got %#v", ops)
	}
	if ops[0].ImagePath != imagePath || ops[0].ReplyToMessageID != "om-source-1" || ops[0].GatewayID != "app-1" {
		t.Fatalf("unexpected final image operation: %#v", ops[0])
	}
}

func TestProjectFinalAssistantBlockNeutralizesUnresolvedLocalMarkdownImage(t *testing.T) {
	projector := NewProjector()
	imagePath := "/tmp/not-resolved-by-preview.png"
	ops := projector.ProjectEvent("chat-1", eventcontract.Event{
		Kind: eventcontract.KindBlockCommitted,
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  "![训练曲线](" + imagePath + ")",
			Final: true,
		},
	})
	if len(ops) != 1 || ops[0].Kind != OperationSendCard {
		t.Fatalf("expected unresolved local image to degrade to a safe card, got %#v", ops)
	}
	if strings.Contains(ops[0].CardBody, "![") || !strings.Contains(ops[0].CardBody, "训练曲线") || !strings.Contains(ops[0].CardBody, "`"+imagePath+"`") {
		t.Fatalf("unexpected local image degradation: %#v", ops[0])
	}
}

func TestProjectFinalAssistantBlockAndResolvedImageComposeSafely(t *testing.T) {
	projector := NewProjector()
	imagePath := writeProjectorFinalImage(t, filepath.Join(t.TempDir(), "diagram.png"))
	event := eventcontract.Event{
		Kind:            eventcontract.KindBlockCommitted,
		SourceMessageID: "om-source-1",
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  "结果如下：\n\n已完成。",
			Final: true,
		},
	}
	ops := projector.ProjectEvent("chat-1", event)
	ops = append(ops, projector.ProjectFinalImagePaths("chat-1", event, []string{imagePath})...)
	if len(ops) != 2 || ops[0].Kind != OperationSendCard || ops[1].Kind != OperationSendImage {
		t.Fatalf("expected safe card followed by image upload, got %#v", ops)
	}
	if strings.Contains(ops[0].CardBody, imagePath) || strings.Contains(ops[0].CardBody, "![") {
		t.Fatalf("local image target leaked into card markdown: %#v", ops[0])
	}
	if ops[1].ImagePath != imagePath || ops[1].ReplyToMessageID != "om-source-1" {
		t.Fatalf("unexpected final image operation: %#v", ops[1])
	}
}

func TestProjectFinalAssistantBlockDowngradesRemoteMarkdownImageToLink(t *testing.T) {
	projector := NewProjector()
	ops := projector.ProjectEvent("chat-1", eventcontract.Event{
		Kind: eventcontract.KindBlockCommitted,
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  "![架构图](https://example.com/diagram.png)",
			Final: true,
		},
	})
	if len(ops) != 1 || ops[0].Kind != OperationSendCard {
		t.Fatalf("expected remote image to stay in a safe card, got %#v", ops)
	}
	if ops[0].CardBody != "[架构图](https://example.com/diagram.png)" {
		t.Fatalf("unexpected remote image downgrade: %#v", ops[0])
	}
}

func TestProjectFinalAssistantBlockLeavesImageSyntaxInsideCodeUntouched(t *testing.T) {
	projector := NewProjector()
	text := "inline `![示例](/tmp/inline.png)`\n\n```md\n![示例](/tmp/fenced.png)\n```"
	ops := projector.ProjectEvent("chat-1", eventcontract.Event{
		Kind: eventcontract.KindBlockCommitted,
		Block: &render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Text:  text,
			Final: true,
		},
	})
	if len(ops) != 1 || ops[0].Kind != OperationSendCard || ops[0].CardBody != text {
		t.Fatalf("expected code examples to remain card text, got %#v", ops)
	}
}

func TestProjectFinalImagePathsDeduplicatesAndRejectsInvalidPaths(t *testing.T) {
	projector := NewProjector()
	imagePath := writeProjectorFinalImage(t, filepath.Join(t.TempDir(), "diagram.png"))
	ops := projector.ProjectFinalImagePaths("chat-1", eventcontract.Event{}, []string{
		imagePath,
		imagePath,
		"relative.png",
		filepath.Join(t.TempDir(), "missing.png"),
	})
	if len(ops) != 1 || ops[0].ImagePath != imagePath {
		t.Fatalf("unexpected validated image operations: %#v", ops)
	}
}

func writeProjectorFinalImage(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	return path
}
