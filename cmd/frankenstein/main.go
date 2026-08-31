// Command frankenstein is a terminal client for Proton Mail and Google
// Calendar.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mhrsntrk/frankenstein-cli/internal/cli"
)

func main() {
	// Ctrl-C cancels the context so an in-flight sync stops cleanly rather
	// than leaving a half-applied cursor.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx))
}
