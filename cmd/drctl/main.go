// Command drctl manages a UniFi Dream Router 7's static DNS records, DHCP
// reservations and device DNS names. Run "drctl --help" for usage.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/liketed/drctl/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.SystemEnv())
	stop()
	os.Exit(code)
}
