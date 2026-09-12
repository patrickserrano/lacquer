package pluginbootstrap

import "os/exec"

// runTool invokes a locating tool through xcrun, which is how Xcode's own
// binaries are addressed without hardcoding a developer-directory path.
func runTool(bin string, args ...string) ([]byte, error) {
	return exec.Command("xcrun", append([]string{bin}, args...)...).Output()
}
