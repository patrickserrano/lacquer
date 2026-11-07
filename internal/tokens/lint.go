package tokens

import (
	"fmt"

	"github.com/patrickserrano/lacquer/internal/config"
)

const (
	// IOSCILintResult preserves the ordinary lint verdict unless lint debt is declared.
	IOSCILintResult = "{{IOS_CI_LINT_RESULT}}"
	// IOSCIFormatCheck wraps the existing formatter check only for declared format debt.
	IOSCIFormatCheck = "{{IOS_CI_FORMAT_CHECK}}"
)

// CILintResult honours the existing relaxation reader's runtime expiry check.
// Lint still runs; only its failure is admitted during an active relaxation.
func CILintResult(cfg *config.Config) string {
	return ciStyleResult(cfg, "lint")
}

// ciStyleResult keeps each tool's reason and runtime expiry under its own key.
func ciStyleResult(cfg *config.Config, key string) string {
	if _, ok := cfg.Baseline.Relax[key]; !ok {
		return "exit $status"
	}
	return fmt.Sprintf(`if ! state=$(RELAX_KEY=%[1]s scripts/docs-relaxation.sh .lacquer.toml); then
            echo "::error::Could not read [baseline.relax].%[1]s."
            exit 1
          fi
          case "$state" in
            relaxed)
              echo "::warning::%[1]s is relaxed by the time-boxed [baseline.relax].%[1]s entry; tool results remain in the log."
              status=0 ;;
            none) ;;
            expired|malformed)
              echo "::error::[baseline.relax].%[1]s is $state. Meet the %[1]s baseline or update the dated entry."
              exit 1 ;;
            *) echo "::error::Unexpected %[1]s relaxation state: $state"; exit 1 ;;
          esac
          exit $status`, key)
}

// CIFormatCheck renders the existing command verbatim for default consumers.
func CIFormatCheck(cfg *config.Config, prefix string) string {
	command := fmt.Sprintf(`"$SWIFTFORMAT_BIN" --lint --config %s.swiftformat %s.`, prefix, prefix)
	if _, ok := cfg.Baseline.Relax["format"]; !ok {
		return command
	}
	return "status=0\n          " + command + " || status=$?\n          " + ciStyleResult(cfg, "format")
}
