package producers

import (
	"os"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox/inboxtest"
)

func TestMain(m *testing.M) {
	os.Exit(inboxtest.Run(m, func() int { return m.Run() }))
}
