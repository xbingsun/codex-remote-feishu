package preview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/render"
)

func TestDriveMarkdownPreviewerResolvesLocalImageWithoutPublishingWebURL(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "output", "diagram.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	previewer := NewDriveMarkdownPreviewer(newFakePreviewAPI(), MarkdownPreviewConfig{
		StatePath:  filepath.Join(root, "state", "preview.json"),
		ProcessCWD: root,
		CacheDir:   filepath.Join(root, "preview-cache"),
	})
	web := &fakeWebPreviewPublisher{baseURL: "http://127.0.0.1:9512/g/grant/?t=token"}
	previewer.SetWebPreviewPublisher(web)

	result, err := previewer.RewriteFinalBlock(context.Background(), MarkdownPreviewRequest{
		SurfaceSessionID: "feishu:app-1:chat:oc_1",
		ChatID:           "oc_1",
		ActorUserID:      "ou_1",
		WorkspaceRoot:    root,
		ThreadCWD:        root,
		Block: render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Final: true,
			Text:  "![训练曲线](output/diagram.png)",
		},
	})
	if err != nil {
		t.Fatalf("RewriteFinalBlock returned error: %v", err)
	}
	canonicalImagePath, err := filepath.EvalSymlinks(imagePath)
	if err != nil {
		t.Fatalf("canonicalize image path: %v", err)
	}
	if result.Block.Text != "" {
		t.Fatalf("resolved image path must not remain in final markdown: %q", result.Block.Text)
	}
	if len(result.ImagePaths) != 1 || result.ImagePaths[0] != canonicalImagePath {
		t.Fatalf("unexpected structured image paths: %#v", result.ImagePaths)
	}
	if strings.Contains(result.Block.Text, "127.0.0.1:9512") || len(web.issuedFor) != 0 {
		t.Fatalf("image markdown must not be published as a web preview: text=%q grants=%#v", result.Block.Text, web.issuedFor)
	}
}

func TestDriveMarkdownPreviewerLimitsAndDeduplicatesLocalImages(t *testing.T) {
	root := t.TempDir()
	var text strings.Builder
	for i := 0; i < finalPreviewImageLimit+1; i++ {
		path := filepath.Join(root, fmt.Sprintf("image-%d.png", i))
		if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
			t.Fatalf("write image %d: %v", i, err)
		}
		fmt.Fprintf(&text, "![image-%d](%s)\n", i, path)
	}
	text.WriteString("![duplicate](" + filepath.Join(root, "image-0.png") + ")")
	previewer := NewDriveMarkdownPreviewer(newFakePreviewAPI(), MarkdownPreviewConfig{
		StatePath:  filepath.Join(root, "state", "preview.json"),
		ProcessCWD: root,
		CacheDir:   filepath.Join(root, "preview-cache"),
	})

	result, err := previewer.RewriteFinalBlock(context.Background(), MarkdownPreviewRequest{
		SurfaceSessionID: "feishu:app-1:chat:oc_1",
		ChatID:           "oc_1",
		ActorUserID:      "ou_1",
		WorkspaceRoot:    root,
		ThreadCWD:        root,
		Block: render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Final: true,
			Text:  text.String(),
		},
	})
	if err != nil {
		t.Fatalf("RewriteFinalBlock returned error: %v", err)
	}
	if len(result.ImagePaths) != finalPreviewImageLimit {
		t.Fatalf("image path count = %d, want %d: %#v", len(result.ImagePaths), finalPreviewImageLimit, result.ImagePaths)
	}
	if strings.Contains(result.Block.Text, "![") {
		t.Fatalf("unsafe image markdown remained after degradation: %q", result.Block.Text)
	}
	if !strings.Contains(result.Block.Text, "[image-4](") {
		t.Fatalf("overflow image must degrade to a regular link: %q", result.Block.Text)
	}
}

func TestDriveMarkdownPreviewerDoesNotReadImageOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	imagePath := filepath.Join(outsideRoot, "private.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write outside image: %v", err)
	}
	previewer := NewDriveMarkdownPreviewer(newFakePreviewAPI(), MarkdownPreviewConfig{
		StatePath:  filepath.Join(root, "state", "preview.json"),
		ProcessCWD: root,
		CacheDir:   filepath.Join(root, "preview-cache"),
	})

	result, err := previewer.RewriteFinalBlock(context.Background(), MarkdownPreviewRequest{
		SurfaceSessionID: "feishu:app-1:chat:oc_1",
		ChatID:           "oc_1",
		ActorUserID:      "ou_1",
		WorkspaceRoot:    root,
		ThreadCWD:        root,
		Block: render.Block{
			Kind:  render.BlockAssistantMarkdown,
			Final: true,
			Text:  "![private](" + imagePath + ")",
		},
	})
	if err != nil {
		t.Fatalf("RewriteFinalBlock returned error: %v", err)
	}
	if len(result.ImagePaths) != 0 {
		t.Fatalf("outside image must not enter structured lane: %#v", result.ImagePaths)
	}
	if strings.Contains(result.Block.Text, "![") || !strings.Contains(result.Block.Text, "[private](") {
		t.Fatalf("outside image must degrade to a regular link: %q", result.Block.Text)
	}
}
