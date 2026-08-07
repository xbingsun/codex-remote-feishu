package preview

import "strings"

const finalPreviewImageLimit = 4

func (p *DriveMarkdownPreviewer) resolveLocalMarkdownImagePath(rawTarget string, req MarkdownPreviewRequest) (string, bool, error) {
	resolvedPath, matched, err := p.resolvePreviewPath(rawTarget, req)
	if err != nil || !matched {
		return "", false, err
	}
	kind, _, ok := previewArtifactMetadata(resolvedPath)
	if !ok || kind != "image" {
		return "", false, nil
	}
	return resolvedPath, true, nil
}

func (r *previewRewriteRuntime) addFinalImagePath(path string) bool {
	if r == nil || strings.TrimSpace(path) == "" {
		return false
	}
	if r.imageSeen == nil {
		r.imageSeen = map[string]bool{}
	}
	path = strings.TrimSpace(path)
	if r.imageSeen[path] {
		return true
	}
	if len(r.imagePaths) >= finalPreviewImageLimit {
		return false
	}
	r.imageSeen[path] = true
	r.imagePaths = append(r.imagePaths, path)
	return true
}

func renderPreviewImageFallback(label, target string) string {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if label == "" {
		return target
	}
	return "[" + label + "](" + target + ")"
}
