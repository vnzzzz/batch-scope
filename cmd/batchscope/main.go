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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"batchscope/internal/app"
	"batchscope/internal/observability"
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

	resolvedDataDir, err := resolveDataDirectory(*dataDir)
	if err != nil {
		return err
	}
	application, err := app.New(app.Config{
		Version: version,
		Commit:  commit,
		DataDir: resolvedDataDir,
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
		attrs := observability.Attrs(observability.Fields{Operation: "serve", BootID: application.BootID()})
		attrs = append(attrs, slog.String("address", server.Addr), slog.String("data_dir", resolvedDataDir))
		slog.LogAttrs(ctx, slog.LevelInfo, "BatchScope listening", attrs...)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		attrs := observability.Attrs(observability.Fields{Operation: "shutdown", BootID: application.BootID()})
		slog.LogAttrs(context.Background(), slog.LevelInfo, "BatchScope stopped", attrs...)
		return err
	case err := <-errCh:
		return err
	}
}

func resolveDataDirectory(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	return resolved, nil
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
