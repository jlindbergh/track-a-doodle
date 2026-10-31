// Command server runs the track-a-doodle HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/api"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is not set")
	}
	martinURL, err := url.Parse(getenv("MARTIN_URL", "http://localhost:3000"))
	if err != nil {
		return fmt.Errorf("parse MARTIN_URL: %w", err)
	}

	st, err := store.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	srv := api.New(st, importer.New(st), martinURL, log)

	var handler http.Handler
	switch mode := os.Getenv("AUTH_MODE"); mode {
	case "dev":
		u, err := st.EnsureUserByEmail(ctx, getenv("DEV_USER_EMAIL", "dev@localhost"))
		if err != nil {
			return err
		}
		log.Warn("AUTH_MODE=dev: all requests are served as one user; never use this outside local development",
			"user", u.Email)
		handler = auth.DevMiddleware(u, srv.Routes())
	default:
		return fmt.Errorf("unsupported AUTH_MODE %q: only \"dev\" is implemented so far", mode)
	}

	httpServer := &http.Server{
		Addr:              getenv("LISTEN_ADDR", ":8080"),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", httpServer.Addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
