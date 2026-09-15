// Package accounts provides private identities and shared opaque sessions.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalid      = errors.New("login must contain 1–128 bytes and password 1–72 bytes")
	ErrConflict     = errors.New("login already exists")
	ErrUnauthorized = errors.New("invalid credentials or session")
)

type User struct {
	ID      string `json:"id"`
	Login   string `json:"login"`
	IsAdmin bool   `json:"is_admin"`
}

type Session struct {
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

type Service struct {
	Pool *pgxpool.Pool
	// Guard is required by the application runtime; isolated identity tests may omit it.
	Guard func(context.Context, pgx.Tx) error
}

func (s Service) mutate(ctx context.Context, change func(pgx.Tx) error) error {
	return database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if s.Guard != nil {
			if err := s.Guard(ctx, tx); err != nil {
				return err
			}
		}
		if err := change(tx); err != nil {
			return err
		}
		if s.Guard != nil {
			return s.Guard(ctx, tx)
		}
		return nil
	})
}

func (s Service) Register(ctx context.Context, login, password string) (User, error) {
	login = strings.TrimSpace(login)
	if login == "" || len(login) > 128 || !utf8.ValidString(login) || len(password) == 0 || len(password) > 72 {
		return User{}, ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var user User
	err = s.mutate(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO users(login,password_hash) VALUES($1,$2) RETURNING id::STRING,login,is_admin`, login, string(hash)).Scan(&user.ID, &user.Login, &user.IsAdmin)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrConflict
	}
	return user, err
}

func (s Service) Login(ctx context.Context, login, password string) (Session, error) {
	var user User
	var hash string
	err := s.Pool.QueryRow(ctx, `SELECT id::STRING,login,is_admin,password_hash FROM users WHERE login=$1`, strings.TrimSpace(login)).Scan(&user.ID, &user.Login, &user.IsAdmin, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	if len(password) == 0 || len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Session{}, ErrUnauthorized
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return Session{}, err
	}
	result := Session{Token: base64.RawURLEncoding.EncodeToString(random[:]), User: user}
	sum := sha256.Sum256([]byte(result.Token))
	err = s.mutate(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+INTERVAL '24 hours') RETURNING expires_at`, sum[:], user.ID).Scan(&result.ExpiresAt)
	})
	return result, err
}

func (s Service) Authenticate(ctx context.Context, token string) (User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return User{}, ErrUnauthorized
	}
	sum := sha256.Sum256([]byte(token))
	var user User
	err = s.Pool.QueryRow(ctx, `SELECT u.id::STRING,u.login,u.is_admin FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, sum[:]).Scan(&user.ID, &user.Login, &user.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	return user, err
}

func (s Service) Logout(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	return s.mutate(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, sum[:])
		return err
	})
}

// EnsureAdmin is a trusted startup operation. Existing credentials must match;
// configuration never silently resets a password or promotes a different account.
func (s Service) EnsureAdmin(ctx context.Context, login, password string) error {
	login = strings.TrimSpace(login)
	if login == "" || len(login) > 128 || !utf8.ValidString(login) || len(password) == 0 || len(password) > 72 {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO users(login,password_hash,is_admin) VALUES($1,$2,true) ON CONFLICT(login) DO NOTHING", login, string(hash)); err != nil {
			return err
		}
		var stored string
		if err := tx.QueryRow(ctx, "SELECT password_hash FROM users WHERE login=$1 FOR UPDATE", login).Scan(&stored); err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) != nil {
			return ErrConflict
		}
		_, err := tx.Exec(ctx, "UPDATE users SET is_admin=true WHERE login=$1", login)
		return err
	})
}
