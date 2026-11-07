package tokens

import (
	"fmt"

	"github.com/patrickserrano/lacquer/internal/config"
)

const (
	// IOSCITestJobTimeout and IOSCITestStepTimeout preserve the existing limits
	// unless the manifest explicitly supplies different ones.
	IOSCITestJobTimeout  = "{{IOS_CI_TEST_JOB_TIMEOUT}}"
	IOSCITestStepTimeout = "{{IOS_CI_TEST_STEP_TIMEOUT}}"
	// IOSPackageResolvedBootstrap follows each existing XcodeGen step; empty
	// unless the project declares one committed source outside its Xcode project.
	IOSPackageResolvedBootstrap = "{{IOS_PACKAGE_RESOLVED_BOOTSTRAP}}"
)

// PackageResolvedBootstrap renders the shared post-generation copy, before
// package caching, strict resolution or builds. It never resolves packages.
func PackageResolvedBootstrap(cfg *config.Config) string {
	if cfg.IOS.PackageResolved == "" {
		return ""
	}
	return fmt.Sprintf(`

      - name: Bootstrap Package.resolved
        run: |
          set -euo pipefail
          source=%q
          project=%q
          resolved="$project/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"
          if [ ! -f "$source" ] || ! git ls-files --error-unmatch -- "$source" >/dev/null 2>&1; then
            echo "::error::The configured package_resolved source $source must exist and be committed."
            exit 1
          fi
          if [ ! -f "$project/project.pbxproj" ]; then
            echo "::error::$project/project.pbxproj is missing after project generation."
            exit 1
          fi
          if git ls-files --error-unmatch -- "$resolved" >/dev/null 2>&1; then
            echo "::error::$resolved is tracked alongside $source. Keep only the configured source committed."
            exit 1
          fi
          mkdir -p "$(dirname "$resolved")"
          cp -f "$source" "$resolved"`, cfg.IOS.PackageResolved, cfg.Project.Xcodeproj)
}
