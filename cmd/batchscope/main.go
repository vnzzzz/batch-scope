package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"batchscope/internal/app"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "serve":
		return serve(args)
	case "version":
		fmt.Printf("batchscope %s (commit=%s)\n", version, commit)
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "HTTP listen address")
	dataDir := fs.String("data-dir", defaultDataDirectory(), "ephemeral data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *listen == "" {
		defaultListen, err := defaultListenAddress()
		if err != nil {
			return err
		}
		*listen = defaultListen
	}

	application, err := app.New(app.Config{
		Version: version,
		Commit:  commit,
		DataDir: *dataDir,
	})
	if err != nil {
		return err
	}
	defer application.Close()

	server := &http.Server{
		Addr:              *listen,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("BatchScope listening", "address", server.Addr, "data_dir", *dataDir)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func defaultListenAddress() (string, error) {
	value := envOrDefault("PORT", "8080")
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid PORT %q: must be an integer from 1 to 65535", value)
	}
	return "0.0.0.0:" + value, nil
}

func defaultDataDirectory() string {
	return envOrDefault("BATCHSCOPE_DATA_DIR", "/tmp/batchscope")
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
