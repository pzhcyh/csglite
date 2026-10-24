// knowledge-preview runs the normal CSGLite server with an isolated app home.
// It intentionally avoids the CLI service/PID and automatic runtime updater.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/server"
)

func main() {
	storage := flag.String("storage", "", "required isolated absolute storage directory")
	listen := flag.String("listen", "127.0.0.1:11437", "loopback preview address")
	flag.Parse()
	if err := run(*storage, *listen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(storage, listen string) error {
	if storage == "" || !filepath.IsAbs(storage) {
		return fmt.Errorf("--storage must be an explicit absolute directory")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("invalid --listen: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("preview must listen on a loopback IP address")
	}
	if err := os.MkdirAll(storage, 0o700); err != nil {
		return fmt.Errorf("creating preview storage: %w", err)
	}
	if err := os.Setenv(config.EnvAppHome, filepath.Clean(storage)); err != nil {
		return err
	}
	// A development preview must not inherit a production cluster join token.
	if err := os.Setenv(server.EnvClusterDisabled, "1"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.ListenAddr = listen
	cfg.AuthCallbackAddr = "127.0.0.1:0"
	fmt.Printf("CSGLite knowledge preview: http://%s/knowledge\nStorage: %s\n", listen, storage)
	return server.New(cfg, "knowledge-preview.1").Run(context.Background())
}
