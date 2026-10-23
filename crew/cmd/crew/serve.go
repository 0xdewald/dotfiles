package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xdewald/dotfiles/crew/internal/api"
	"github.com/0xdewald/dotfiles/crew/internal/mcpserver"
)

// crew serve [--addr A]: run the server in this terminal. crew serve start|stop|restart|logs
// manage it in the background instead.
func (a *app) serve(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "start":
			return a.serveStart(args[1:])
		case "stop":
			return a.serveStop()
		case "restart":
			if err := a.serveStop(); err != nil {
				return err
			}
			return a.serveStart(args[1:])
		case "logs":
			return a.serveLogs()
		}
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7767", "listen address")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("serve: %w (is crew serve already running? see crew status)", err)
	}
	if err := writeServerInfo(a.b.Home, ln.Addr().String()); err != nil {
		return err
	}
	defer removeServerInfo(a.b.Home)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM) // not SIGHUP: nohup must keep it running
	go func() { <-stop; removeServerInfo(a.b.Home); os.Exit(0) }()
	fmt.Fprintln(a.stderr, "crew: serving", a.b.Home, "on http://"+ln.Addr().String(), "build", build())
	go func() { // agents idle for too long go stale
		for range time.Tick(time.Minute) {
			a.b.Sweep()
		}
	}()
	api.Build = build()
	return http.Serve(ln, api.Handler(a.b))
}

func (a *app) mcp() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return mcpserver.Run(ctx, a.b)
}
