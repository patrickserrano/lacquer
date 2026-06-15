package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: harness <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: sync, status")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		fmt.Println("sync: not yet implemented")
	case "status":
		fmt.Println("status: not yet implemented")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}
