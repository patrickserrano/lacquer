// Package version reads the lacquer repo's current version from its VERSION file.
package version

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Read returns the integer version recorded in <lacquerRoot>/VERSION.
func Read(lacquerRoot string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(lacquerRoot, "VERSION"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}
