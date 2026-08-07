package preview

import "os"

func (p *DriveMarkdownPreviewer) resolvePreviewPath(rawTarget string, req MarkdownPreviewRequest) (string, bool, error) {
	target, ok := normalizePreviewReferenceTarget(rawTarget)
	if !ok {
		return "", false, nil
	}

	cleanTarget, _, _ := splitPreviewLocationSuffix(target)
	if _, _, ok := previewArtifactMetadata(cleanTarget); !ok {
		return "", false, nil
	}

	roots := previewAllowedRoots(req.ThreadCWD, req.WorkspaceRoot, p.config.ProcessCWD)
	candidates := previewPathCandidates(cleanTarget, roots)
	for _, candidate := range candidates {
		resolved, err := previewCanonicalPath(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", true, err
		}
		if !previewPathWithinAnyRoot(resolved, roots) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", true, err
		}
		if info.IsDir() {
			continue
		}
		return resolved, true, nil
	}
	return "", false, nil
}
