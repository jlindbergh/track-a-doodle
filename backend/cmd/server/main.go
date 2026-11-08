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
	"strconv"
	"syscall"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/account"
	"github.com/jlindbergh/track-a-doodle/backend/internal/activitysync"
	"github.com/jlindbergh/track-a-doodle/backend/internal/api"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/mail"
	"github.com/jlindbergh/track-a-doodle/backend/internal/secretbox"
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

	im := importer.New(st)
	srv := api.New(st, im, martinURL, log)

	if key := os.Getenv("SECRETS_KEY"); key != "" {
		box, err := secretbox.NewFromBase64(key)
		if err != nil {
			return fmt.Errorf("SECRETS_KEY: %w", err)
		}
		syncer := activitysync.New(activitysync.Config{Store: st, Importer: im, Box: box, Logger: log})
		srv.EnableConnections(syncer)
		go syncer.Run(ctx)
		log.Info("Garmin Connect sync is on")
	} else {
		log.Info("SECRETS_KEY is not set, so users can't connect Garmin Connect")
	}

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
	case "password":
		cfg, err := accountsConfig(st, log)
		if err != nil {
			return err
		}
		srv.EnableAccounts(cfg)
		handler = srv.SessionMiddleware(st.UserBySession, srv.Routes())
		go purgeLoop(ctx, st, log)
	default:
		return fmt.Errorf("unsupported AUTH_MODE %q: want \"password\" or \"dev\"", mode)
	}
	handler = api.CrossOriginProtection(handler)

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

// accountsConfig sets up email + password accounts from the environment.
func accountsConfig(st *store.Store, log *slog.Logger) (api.AccountsConfig, error) {
	baseURL, err := requiredURL("APP_BASE_URL")
	if err != nil {
		return api.AccountsConfig{}, err
	}
	tlsMode, err := mail.ParseTLSMode(getenv("SMTP_TLS", "starttls"))
	if err != nil {
		return api.AccountsConfig{}, err
	}
	smtpAddr := os.Getenv("SMTP_ADDR")
	from := os.Getenv("MAIL_FROM")
	if smtpAddr == "" || from == "" {
		return api.AccountsConfig{}, errors.New("AUTH_MODE=password needs SMTP_ADDR and MAIL_FROM to send verification emails")
	}
	sender, err := mail.NewSMTPSender(mail.SMTPConfig{
		Addr:     smtpAddr,
		TLS:      tlsMode,
		Username: os.Getenv("SMTP_USER"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     from,
	})
	if err != nil {
		return api.AccountsConfig{}, err
	}
	mailPerHour, err := strconv.Atoi(getenv("MAIL_MAX_PER_HOUR", "100"))
	if err != nil || mailPerHour < 1 {
		return api.AccountsConfig{}, fmt.Errorf("MAIL_MAX_PER_HOUR must be a positive number, not %q", os.Getenv("MAIL_MAX_PER_HOUR"))
	}
	cookieSecure := getenv("COOKIE_SECURE", "true") != "false"
	if !cookieSecure {
		log.Warn("COOKIE_SECURE=false: session cookies are sent over plain HTTP; only use this on localhost")
	}
	if tlsMode == mail.TLSNone {
		log.Warn("SMTP_TLS=none: emails are sent unencrypted; only use this with a local mail catcher")
	}
	return api.AccountsConfig{
		Accounts:     account.New(st, sender, account.Config{BaseURL: baseURL, MailPerHour: mailPerHour}, log),
		CookieSecure: cookieSecure,
		TrustProxy:   os.Getenv("TRUST_PROXY_HEADERS") == "true",
	}, nil
}

func requiredURL(key string) (*url.URL, error) {
	v := os.Getenv(key)
	if v == "" {
		return nil, fmt.Errorf("%s is not set", key)
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%s must be an http(s) URL, not %q", key, v)
	}
	return u, nil
}

// purgeLoop deletes expired sessions and tokens and stale sign-ups every
// hour until ctx is done.
func purgeLoop(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		c, err := st.PurgeExpired(ctx, account.UnverifiedTTL)
		if err != nil && ctx.Err() == nil {
			log.Error("purge expired accounts data", "err", err)
		} else if c.Users > 0 || c.Sessions > 0 || c.Tokens > 0 {
			log.Info("purged expired accounts data", "users", c.Users, "sessions", c.Sessions, "tokens", c.Tokens)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
