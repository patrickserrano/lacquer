package assets

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// renderDocumentationHook leaves the default template untouched. Opting in
// replaces only the comment marker with the exact legacy entry.
func renderDocumentationHook(body []byte, enabled bool, prefix string) ([]byte, error) {
	if !enabled {
		return body, nil
	}
	marker := []byte("      # lacquer:documentation-hook\n")
	count := bytes.Count(body, marker)
	if count == 0 {
		return body, nil
	}
	if count != 1 {
		return nil, fmt.Errorf("duplicate optional documentation hook marker")
	}
	entry := strings.ReplaceAll(legacyDocumentationHook, "{{COMPONENT_PREFIX}}", prefix)
	return bytes.Replace(body, marker, []byte(entry), 1), nil
}

const legacyDocumentationHook = "      # The documentation baseline, and now the whole of it. It used to be the\n" +
	"      # cheap half of a pair, with a CI `Docs` job building the DocC archive;\n" +
	"      # that job and the script behind it are both gone, so nothing anywhere\n" +
	"      # else checks this. There is no CI twin to defer to and no second chance\n" +
	"      # after the push — a `|| true` here would retire the baseline, not soften\n" +
	"      # it.\n" +
	"      - id: swiftlint-docs\n" +
	"        name: SwiftLint (documentation)\n" +
	"        entry: scripts/docs-hook.sh scripts/precommit-swift.sh swiftlint-docs\n" +
	"        verbose: true\n" +
	"        language: system\n" +
	"        types: [swift]\n" +
	"        files: ^{{COMPONENT_PREFIX}}\n" +
	"\n"

// documentationReaderNeeded changes only iOS-only projects. Other profiles
// retain their existing core assets and bytes, including mixed-profile repos.
func documentationReaderNeeded(cfg *config.Config) bool {
	ios := false
	for _, component := range cfg.Components {
		for _, profile := range component.Profiles {
			if profile != "ios" {
				return true
			}
			ios = true
		}
	}
	if !ios || cfg.IOS.DocumentationHook {
		return true
	}
	_, lint := cfg.Baseline.Relax["lint"]
	_, format := cfg.Baseline.Relax["format"]
	// A declared style consumer can live in a project-owned workflow too.
	return lint || format
}
