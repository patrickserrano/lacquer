package console

import (
	"os"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}
