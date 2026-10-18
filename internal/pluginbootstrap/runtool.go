package pluginbootstrap

import "github.com/patrickserrano/lacquer/internal/procgroup"

// runTool invokes a locating tool through xcrun, which is how Xcode's own
// binaries are addressed without hardcoding a developer-directory path.
func runTool(bin string, args ...string) ([]byte, error) {
	return procgroup.New(procgroup.Xcrun, "xcrun", append([]string{bin}, args...)...).Output()
}
