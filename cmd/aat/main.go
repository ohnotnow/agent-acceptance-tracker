package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ohnotnow/agent-acceptance-tracker/internal/aat"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":{"code":"internal","message":%q}}`+"\n", err.Error())
		os.Exit(1)
	}
	os.Exit(aat.Run(context.Background(), os.Args[1:], dir, aat.Streams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}))
}
