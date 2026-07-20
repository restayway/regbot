package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/restayway/regbot/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Execute(ctx, os.Stdout, os.Stderr, os.Args[1:]))
}
