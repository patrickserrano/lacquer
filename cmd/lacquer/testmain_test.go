package main

import (
	"os"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/inbox/inboxtest"
)

func TestMain(m *testing.M) {
	os.Exit(inboxtest.Run(m, func() int { return gittest.Run(m) }))
}
