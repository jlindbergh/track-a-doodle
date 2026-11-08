package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound means the user, token or session doesn't exist or has expired.
var ErrNotFound = errors.New("not found")

// Account is a user with what is needed to sign them in.
type Account struct {
	User
	// PasswordHash is "" for users who can't sign in with a password.
	PasswordHash string
	Verified     bool
}

// SendLimit caps how often one address is emailed.
type SendLimit struct {
	// MinInterval is the shortest time between two emails.
	MinInterval time.Duration
	// PerDay is the most emails in 24 hours.
	PerDay int
}

// Registration is a sign-up about to be stored.
type Registration struct {
	Email        string
	PasswordHash string
	// TokenHash is the SHA-256 of the verification token to email.
	TokenHash []byte
	ExpiresAt time.Time
	Limit     SendLimit
}

// RegisterOutcome says what Register did, and so which email to send.
type RegisterOutcome int

const (
	// Pending means the user was created, or an unverified one got the new
	// password, and the verification token was stored. Send it.
	Pending RegisterOutcome = iota
	// Exists means the address already belongs to an account, which was
	// left alone. Tell its owner.
	Exists
	// Limited means the address was emailed too recently and nothing was
	// changed. Send nothing.
	Limited
)

// Register stores a sign-up. An unverified account with a password is taken
// over: it gets the new password and a new token, and older tokens stop
// working. Any other existing account is never changed. Each outcome but
// Limited counts as one email sent to the address.
func (s *Store) Register(ctx context.Context, reg Registration) (RegisterOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize sign-ups per address, so the send limit and the "only one
	// live token" rule hold under concurrent requests.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('register:' || $1, 0))`, reg.Email); err != nil {
		return 0, fmt.Errorf("lock registration: %w", err)
	}

	var (
		userID    string
		claimable bool
	)
	err = tx.QueryRow(ctx, `
		SELECT id, email_verified_at IS NULL AND password_hash IS NOT NULL
		  FROM users WHERE email = $1`, reg.Email).Scan(&userID, &claimable)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
			reg.Email, reg.PasswordHash).Scan(&userID); err != nil {
			return 0, fmt.Errorf("create user: %w", err)
		}
	case err != nil:
		return 0, fmt.Errorf("find user: %w", err)
	default:
		ok, err := allowSend(ctx, tx, userID, reg.Limit)
		if err != nil {
			return 0, err
		}
		if !ok {
			return Limited, nil
		}
		if !claimable {
			if err := recordSend(ctx, tx, userID); err != nil {
				return 0, err
			}
			return Exists, commit(ctx, tx, "registration")
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, reg.PasswordHash); err != nil {
			return 0, fmt.Errorf("update password: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM email_tokens WHERE user_id = $1 AND purpose = 'verify'`, userID); err != nil {
		return 0, fmt.Errorf("delete old tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, 'verify', $3)`,
		reg.TokenHash, userID, reg.ExpiresAt); err != nil {
		return 0, fmt.Errorf("store token: %w", err)
	}
	if err := recordSend(ctx, tx, userID); err != nil {
		return 0, err
	}
	return Pending, commit(ctx, tx, "registration")
}

func allowSend(ctx context.Context, tx pgx.Tx, userID string, l SendLimit) (bool, error) {
	var recent, today int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE sent_at > now() - make_interval(secs => $2)),
		       count(*) FILTER (WHERE sent_at > now() - interval '24 hours')
		  FROM email_sends WHERE user_id = $1`,
		userID, l.MinInterval.Seconds()).Scan(&recent, &today)
	if err != nil {
		return false, fmt.Errorf("count sent emails: %w", err)
	}
	return recent == 0 && today < l.PerDay, nil
}

func recordSend(ctx context.Context, tx pgx.Tx, userID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO email_sends (user_id) VALUES ($1)`, userID); err != nil {
		return fmt.Errorf("record sent email: %w", err)
	}
	return nil
}

func commit(ctx context.Context, tx pgx.Tx, what string) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", what, err)
	}
	return nil
}

const accountColumns = `id, email, display_name, coalesce(password_hash, ''), email_verified_at IS NOT NULL`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Email, &a.DisplayName, &a.PasswordHash, &a.Verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// AccountByEmail returns the account with the given (normalized) email.
func (s *Store) AccountByEmail(ctx context.Context, email string) (Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM users WHERE email = $1`, email))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Account{}, fmt.Errorf("find account: %w", err)
	}
	return a, err
}

// AccountByVerifyToken returns the account a live verification token
// belongs to, without using the token up.
func (s *Store) AccountByVerifyToken(ctx context.Context, tokenHash []byte) (Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, `
		SELECT `+accountColumns+`
		  FROM email_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = $1 AND t.purpose = 'verify' AND t.expires_at > now()`, tokenHash))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Account{}, fmt.Errorf("find verification token: %w", err)
	}
	return a, err
}

// Verify uses up a live verification token, marks its user's email as
// verified and starts a session for them. It returns ErrNotFound if the
// token is gone, e.g. replaced by a newer sign-up since it was looked up.
func (s *Store) Verify(ctx context.Context, tokenHash, sessionHash []byte, sessionExpires time.Time) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin verification: %w", err)
	}
	defer tx.Rollback(ctx)

	var u User
	err = tx.QueryRow(ctx, `
		WITH used AS (
			DELETE FROM email_tokens
			 WHERE token_hash = $1 AND purpose = 'verify' AND expires_at > now()
			RETURNING user_id
		)
		UPDATE users SET email_verified_at = now()
		  FROM used WHERE users.id = used.user_id
		RETURNING id, email, display_name`, tokenHash).Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("use verification token: %w", err)
	}
	if err := insertSession(ctx, tx, u.ID, sessionHash, sessionExpires); err != nil {
		return User{}, err
	}
	return u, commit(ctx, tx, "verification")
}

// CreateSession starts a session for userID.
func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error {
	return insertSession(ctx, s.pool, userID, tokenHash, expiresAt)
}

func insertSession(ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, userID string, tokenHash []byte, expiresAt time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// UserBySession returns the user of a live session. Only verified users
// have sessions.
func (s *Store) UserBySession(ctx context.Context, tokenHash []byte) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name
		  FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find session: %w", err)
	}
	return u, nil
}

// DeleteSession ends a session. Ending one that doesn't exist is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// UserByEmail returns the user with the given email.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	a, err := s.AccountByEmail(ctx, email)
	return a.User, err
}

// PurgeCounts says what PurgeExpired deleted.
type PurgeCounts struct {
	Tokens, Sessions, EmailSends, Users int64
}

// PurgeExpired deletes expired tokens and sessions, the email log older
// than a day, and sign-ups that were never verified and are older than
// unverifiedAge. Users without a password (dev mode) are never purged, nor
// is anyone with activities.
func (s *Store) PurgeExpired(ctx context.Context, unverifiedAge time.Duration) (PurgeCounts, error) {
	var c PurgeCounts
	for _, q := range []struct {
		n   *int64
		sql string
		arg []any
	}{
		{&c.Tokens, `DELETE FROM email_tokens WHERE expires_at <= now()`, nil},
		{&c.Sessions, `DELETE FROM sessions WHERE expires_at <= now()`, nil},
		{&c.EmailSends, `DELETE FROM email_sends WHERE sent_at <= now() - interval '24 hours'`, nil},
		{&c.Users, `
			DELETE FROM users u
			 WHERE u.email_verified_at IS NULL
			   AND u.password_hash IS NOT NULL
			   AND u.created_at <= now() - make_interval(secs => $1)
			   AND NOT EXISTS (SELECT 1 FROM email_tokens t WHERE t.user_id = u.id)
			   AND NOT EXISTS (SELECT 1 FROM activities a WHERE a.user_id = u.id)`,
			[]any{unverifiedAge.Seconds()}},
	} {
		tag, err := s.pool.Exec(ctx, q.sql, q.arg...)
		if err != nil {
			return c, fmt.Errorf("purge: %w", err)
		}
		*q.n = tag.RowsAffected()
	}
	return c, nil
}

// DeleteUser deletes a user and, through the foreign keys, everything that
// belongs to them: activities, connections, sessions and tokens.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}
