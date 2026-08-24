package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Clock func() time.Time

type Service struct {
	store *repository.Store
	ttl   time.Duration
	now   Clock
}

func New(store *repository.Store, ttl time.Duration, now Clock) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, ttl: ttl, now: now}
}

func HashPassword(password string) (string, error) {
	if len(password) < 10 {
		return "", fmt.Errorf("%w: password must contain at least 10 characters", domain.ErrValidation)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (s *Service) EnsureBootstrapOperator(ctx context.Context, email, password string) error {
	_, err := s.store.UserByEmail(ctx, email)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.store.CreateUser(ctx, domain.User{
		Email: email, PasswordHash: hash, Name: "Bootstrap Operator", Role: domain.RoleOperator,
		Active: true, CreatedAt: s.now().UTC(),
	})
	return err
}

type LoginResult struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      domain.User `json:"user"`
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.store.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || !user.Active {
		return LoginResult{}, fmt.Errorf("%w: invalid credentials", domain.ErrUnauthorized)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return LoginResult{}, fmt.Errorf("%w: invalid credentials", domain.ErrUnauthorized)
	}
	token, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	session := domain.Session{
		UserID: user.ID, TokenHash: TokenHash(token), ExpiresAt: now.Add(s.ttl), CreatedAt: now,
	}
	if _, err := s.store.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: session.ExpiresAt, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, error) {
	if token == "" {
		return domain.User{}, fmt.Errorf("%w: missing token", domain.ErrUnauthorized)
	}
	_, user, err := s.store.SessionUserByTokenHash(ctx, TokenHash(token), s.now().UTC())
	if err != nil {
		return domain.User{}, fmt.Errorf("%w: invalid or expired session", domain.ErrUnauthorized)
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("%w: missing token", domain.ErrUnauthorized)
	}
	tokenHash := TokenHash(token)
	now := s.now().UTC()
	session, user, err := s.store.RevokeSessionForLogout(ctx, tokenHash, now)
	if err != nil {
		return err
	}
	_, err = s.store.AppendAudit(ctx, domain.AuditEvent{
		ActorID: user.ID, ActorRole: user.Role, Action: "session.logout", ObjectType: "session",
		ObjectID: repository.AuditObjectID(session.ID), Result: "success",
		RequestID: "logout:" + tokenHash[:12], CreatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("record logout audit: %w", err)
	}
	return nil
}

func (s *Service) RevokeExpired(ctx context.Context) (int64, error) {
	return s.store.RevokeExpiredSessions(ctx, s.now().UTC())
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
