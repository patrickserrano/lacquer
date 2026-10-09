package fleet

import (
	"errors"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/swiftcomponents"
)

// strayState counts the project's .swift files under no declared Swift
// component, and whether that blocks today (#522 U4), exactly as
// `lacquer audit` computes it. Like audit, a project whose files cannot be
// listed (no git work tree, no git) is not checked rather than failed: the sweep's gate must equal audit's.
func strayState(root string, cfg *config.Config, now time.Time) (int, bool, error) {
	r, err := swiftcomponents.Check(root, cfg, now)
	if errors.Is(err, swiftcomponents.ErrCannotList) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return len(r.Stray), r.Blocks(), nil
}
