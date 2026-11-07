package tokens

import (
	"fmt"

	"github.com/patrickserrano/lacquer/internal/config"
)

const (
	// IOSCILintResult preserves the ordinary lint verdict unless lint debt is declared.
	IOSCILintResult = "{{IOS_CI_LINT_RESULT}}"
	// IOSCIFormatCheck wraps the existing formatter check only for declared lint debt.
	IOSCIFormatCheck = "{{IOS_CI_FORMAT_CHECK}}"
)

// CILintResult honours the existing relaxation reader's runtime expiry check.
// Lint still runs; only its failure is admitted during an active relaxation.
func CILintResult(cfg *config.Config) string {
	if _, ok := cfg.Baseline.Relax["lint"]; !ok {
		return "exit $status"
	}
	return `if ! state=$(RELAX_KEY=lint scripts/docs-relaxation.sh .lacquer.toml); then
            echo "::error::Could not read [baseline.relax].lint."
            exit 1
          fi
          case "$state" in
            relaxed)
              echo "::warning::lint is relaxed by the time-boxed [baseline.relax].lint entry; lint results remain in the log."
              status=0 ;;
            none) ;;
            expired|malformed)
              echo "::error::[baseline.relax].lint is $state. Meet the lint baseline or update the dated entry."
              exit 1 ;;
            *) echo "::error::Unexpected lint relaxation state: $state"; exit 1 ;;
          esac
          exit $status`
}

// CIFormatCheck renders the existing command verbatim for default consumers.
func CIFormatCheck(cfg *config.Config, prefix string) string {
	command := fmt.Sprintf(`"$SWIFTFORMAT_BIN" --lint --config %s.swiftformat %s.`, prefix, prefix)
	if _, ok := cfg.Baseline.Relax["lint"]; !ok {
		return command
	}
	return "status=0\n          " + command + " || status=$?\n          " + CILintResult(cfg)
}
