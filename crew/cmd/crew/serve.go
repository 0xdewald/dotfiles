package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"github.com/0xdewald/dotfiles/crew/internal/api"
	"github.com/0xdewald/dotfiles/crew/internal/mcpserver"
)

func (a *app) serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7767", "listen address")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	fmt.Fprintln(a.stderr, "crew: serving", a.b.Home, "on http://"+*addr)
	return http.ListenAndServe(*addr, api.Handler(a.b))
}

func (a *app) mcp() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return mcpserver.Run(ctx, a.b)
}
