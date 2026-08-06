//go:build upgrade_shim

package install

import (
	"fmt"
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/upgradeshim"
)

type UpgradeShimEntrypointOptions struct {
	EntrypointPath   string
	InstallStatePath string
	InstanceID       string
}

func UpgradeShimSidecarPath(entrypointPath string) string {
	return upgradeshim.SidecarPath(entrypointPath)
}

func WriteUpgradeShimEntrypoint(UpgradeShimEntrypointOptions) error {
	return fmt.Errorf("nested upgrade shim entrypoint creation is unavailable in the upgrade shim build")
}

func PrepareUpgradeHelperShim(statePath, _ string) (string, error) {
	if strings.TrimSpace(statePath) == "" {
		return "", fmt.Errorf("state path is required")
	}
	return "", fmt.Errorf("nested upgrade shim preparation is unavailable in the upgrade shim build")
}
