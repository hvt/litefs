package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/superfly/litefs"
	"github.com/superfly/litefs/consul"
	"github.com/superfly/litefs/fuse"
	"github.com/superfly/litefs/http"
)

func main() {
	log.SetFlags(0)

	if err := run(context.Background()); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (err error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	debug := flag.Bool("debug", false, "print debug information")
	addr := flag.String("addr", ":20202", "http bind address")
	consulURL := flag.String("consul-url", "", "")

	flag.Parse()
	if *consulURL == "" {
		return fmt.Errorf("required: --consul-url URL")
	}

	// First argument is the mount point for the file system.
	mountDir := flag.Arg(0)
	if mountDir == "" {
		return fmt.Errorf("usage: litefs MOUNTPOINT")
	} else if mountDir, err = filepath.Abs(mountDir); err != nil {
		return fmt.Errorf("abs: %w", err)
	}

	// TEMP: Determine advertised URL in a better way.
	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("cannot split addr: %w", err)
	}
	advertiseURL := fmt.Sprintf("http://localhost:%s", port)

	// Setup Consul connection.
	leaser := consul.NewLeaser(*consulURL)
	leaser.AdvertiseURL = advertiseURL
	if err := leaser.Open(); err != nil {
		return fmt.Errorf("cannot connect to consul: %w", err)
	}

	// Create a store to manage internal data.
	dir, file := filepath.Split(mountDir)
	store := litefs.NewStore(filepath.Join(dir, "."+file))
	store.Client = http.NewClient()
	store.Leaser = leaser
	if err := store.Open(); err != nil {
		return fmt.Errorf("cannot open store: %w", err)
	}

	// Build the file system to interact with the store.
	fs := fuse.NewFileSystem(mountDir, store)
	fs.Debug = *debug
	if err := fs.Mount(); err != nil {
		return fmt.Errorf("cannot open file system: %s", err)
	}
	defer fs.Unmount()

	// Attach file system to store so it can invalidate the page cache.
	store.InodeNotifier = fs

	log.Printf("LiteFS mounted to: %s", mountDir)

	// Build the HTTP server to provide an API.
	server := http.NewServer(store, *addr)
	if err := server.Open(); err != nil {
		return fmt.Errorf("cannot open http server: %w", err)
	}
	defer server.Close()

	log.Printf("http server listening on: %s", server.URL())

	// Wait for signal before exiting.
	<-ctx.Done()
	fmt.Println("received CTRL-C, exiting")

	if err := fs.Unmount(); err != nil {
		return fmt.Errorf("cannot unmount: %w", err)
	}
	fmt.Println("unmount successful")

	return nil
}
