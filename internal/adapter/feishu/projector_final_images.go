package feishu

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
)

// ProjectFinalImagePaths projects local image paths that the preview layer has
// already resolved and constrained to the active workspace or thread roots.
func (p *Projector) ProjectFinalImagePaths(chatID string, event eventcontract.Event, imagePaths []string) []Operation {
	event = event.Normalized()
	if _, isDriveComment := parseDriveCommentSourceMessageID(firstNonEmpty(event.SourceMessageID, event.Meta.SourceMessageID)); isDriveComment {
		return nil
	}
	ops := make([]Operation, 0, len(imagePaths))
	seen := map[string]bool{}
	for _, imagePath := range imagePaths {
		imagePath = strings.TrimSpace(imagePath)
		if imagePath == "" || seen[imagePath] || !isSendableFinalImagePath(imagePath) {
			continue
		}
		seen[imagePath] = true
		ops = append(ops, Operation{
			Kind:             OperationSendImage,
			GatewayID:        event.GatewayID,
			SurfaceSessionID: event.SurfaceSessionID,
			ChatID:           chatID,
			ReplyToMessageID: replyToMessageIDForEvent(event),
			ImagePath:        filepath.Clean(imagePath),
		})
	}
	return ops
}

func isSendableFinalImagePath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
