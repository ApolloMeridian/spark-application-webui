package localauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"spark-control-center/backend/internal/domain"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthorized       = errors.New("authentication is required")
	ErrForbidden          = errors.New("administrator permission is required")
	ErrNamespaceForbidden = errors.New("namespace access is not permitted")
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameExists     = errors.New("username already exists")
	ErrLastAdmin          = errors.New("the last active administrator cannot be removed, disabled, or demoted")
	ErrSelfDelete         = errors.New("you cannot delete your own account")
	ErrInvalidInput       = errors.New("invalid user input")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

type Store interface {
	BootstrapInitialAdmin(context.Context, domain.User, string, string) error
	FindUserCredentials(context.Context, string) (domain.User, string, error)
	FindUserByID(context.Context, string) (domain.User, error)
	ListUsers(context.Context) ([]domain.User, error)
	CreateUser(context.Context, domain.User, string, string) error
	UpdateUser(context.Context, domain.User, *string) error
	DeleteUser(context.Context, string) error
	CreateSession(context.Context, domain.UserSession) error
	FindSession(context.Context, string, time.Time) (domain.User, domain.UserSession, error)
	DeleteSession(context.Context, string) error
	DeleteSessionsForUser(context.Context, string) error
	UpsertOIDCUser(context.Context, domain.User, string, string, string, bool) (domain.User, error)
}

type Service struct {
	store      Store
	ttl        time.Duration
	bcryptCost int
	dummyHash  []byte
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Email       string
	Role        domain.UserRole
	Namespaces  []string
	Password    string
}

type UpdateUserInput struct {
	DisplayName *string
	Email       *string
	Role        *domain.UserRole
	Namespaces  *[]string
	Disabled    *bool
	Password    *string
}

type UpdateProfileInput struct {
	DisplayName     *string
	Email           *string
	CurrentPassword string
	NewPassword     string
}

type OIDCIdentity struct {
	Issuer      string
	Subject     string
	Username    string
	DisplayName string
	Email       string
	Role        domain.UserRole
	AutoCreate  bool
}

func New(store Store, ttl time.Duration, bcryptCost int) *Service {
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("spark-control-center-dummy-password"), bcryptCost)
	return &Service{store: store, ttl: ttl, bcryptCost: bcryptCost, dummyHash: dummyHash}
}

func (s *Service) Bootstrap(ctx context.Context, username, password, displayName string) error {
	username, normalized, err := validateUsername(username)
	if err != nil {
		return fmt.Errorf("initial administrator: %w", err)
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("initial administrator: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash initial administrator password: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	user := domain.User{ID: newID(), Username: username, DisplayName: cleanDisplayName(displayName, username), Role: domain.RoleAdmin, AuthSource: "local", CreatedAt: now, UpdatedAt: now}
	return s.store.BootstrapInitialAdmin(ctx, user, normalized, string(hash))
}

func (s *Service) Login(ctx context.Context, username, password string) (domain.User, string, time.Time, error) {
	_, normalized, err := validateUsername(username)
	if err != nil || password == "" {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	user, passwordHash, err := s.store.FindUserCredentials(ctx, normalized)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		if errors.Is(err, ErrUserNotFound) {
			return domain.User{}, "", time.Time{}, ErrInvalidCredentials
		}
		return domain.User{}, "", time.Time{}, err
	}
	if user.Disabled || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	return s.createSession(ctx, user)
}

func (s *Service) LoginOIDC(ctx context.Context, identity OIDCIdentity) (domain.User, string, time.Time, error) {
	username, normalized, err := validateUsername(identity.Username)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	if identity.Issuer == "" || identity.Subject == "" {
		return domain.User{}, "", time.Time{}, fmt.Errorf("%w: OIDC issuer and subject are required", ErrInvalidInput)
	}
	if err := validateRole(identity.Role); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	email, err := cleanEmail(identity.Email)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	candidate := domain.User{ID: newID(), Username: username, DisplayName: cleanDisplayName(identity.DisplayName, username), Email: email, Role: identity.Role, AuthSource: "oidc", CreatedAt: now, UpdatedAt: now}
	user, err := s.store.UpsertOIDCUser(ctx, candidate, normalized, identity.Issuer, identity.Subject, identity.AutoCreate)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) && !identity.AutoCreate {
			return domain.User{}, "", time.Time{}, ErrForbidden
		}
		return domain.User{}, "", time.Time{}, err
	}
	if user.Disabled {
		return domain.User{}, "", time.Time{}, ErrForbidden
	}
	return s.createSession(ctx, user)
}

func (s *Service) createSession(ctx context.Context, user domain.User) (domain.User, string, time.Time, error) {
	token, err := randomToken(32)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(s.ttl)
	if err := s.store.CreateSession(ctx, domain.UserSession{TokenHash: hashToken(token), UserID: user.ID, ExpiresAt: expiresAt}); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	return user, token, expiresAt, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, domain.UserSession, error) {
	if strings.TrimSpace(token) == "" {
		return domain.User{}, domain.UserSession{}, ErrUnauthorized
	}
	user, session, err := s.store.FindSession(ctx, hashToken(token), time.Now().UTC())
	if err != nil || user.Disabled {
		if errors.Is(err, ErrUserNotFound) || user.Disabled {
			return domain.User{}, domain.UserSession{}, ErrUnauthorized
		}
		return domain.User{}, domain.UserSession{}, err
	}
	return user, session, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashToken(token))
}

func (s *Service) ListUsers(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if actor.Role != domain.RoleAdmin {
		return nil, ErrForbidden
	}
	return s.store.ListUsers(ctx)
}

func (s *Service) CreateUser(ctx context.Context, actor domain.User, input CreateUserInput) (domain.User, error) {
	if actor.Role != domain.RoleAdmin {
		return domain.User{}, ErrForbidden
	}
	username, normalized, err := validateUsername(input.Username)
	if err != nil {
		return domain.User{}, err
	}
	if err := validateRole(input.Role); err != nil {
		return domain.User{}, err
	}
	if err := validatePassword(input.Password); err != nil {
		return domain.User{}, err
	}
	email, err := cleanEmail(input.Email)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.bcryptCost)
	if err != nil {
		return domain.User{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	user := domain.User{ID: newID(), Username: username, DisplayName: cleanDisplayName(input.DisplayName, username), Email: email, Role: input.Role, Namespaces: cleanNamespaces(input.Namespaces), AuthSource: "local", CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateUser(ctx, user, normalized, string(hash)); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Service) UpdateUser(ctx context.Context, actor domain.User, id string, input UpdateUserInput) (domain.User, error) {
	if actor.Role != domain.RoleAdmin {
		return domain.User{}, ErrForbidden
	}
	user, err := s.store.FindUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if actor.ID == id && ((input.Role != nil && *input.Role != user.Role) || (input.Disabled != nil && *input.Disabled)) {
		return domain.User{}, fmt.Errorf("%w: use another administrator account to change your own role or disable your account", ErrInvalidInput)
	}
	if actor.ID == id && input.Password != nil && *input.Password != "" {
		return domain.User{}, fmt.Errorf("%w: change your own password from the profile page", ErrInvalidInput)
	}
	if user.AuthSource == "oidc" && input.Password != nil && *input.Password != "" {
		return domain.User{}, fmt.Errorf("%w: OIDC users do not have a local password", ErrInvalidInput)
	}
	if user.AuthSource == "oidc" && input.Role != nil && *input.Role != user.Role {
		return domain.User{}, fmt.Errorf("%w: OIDC user roles are managed by group mapping", ErrInvalidInput)
	}
	passwordHash, sensitive, err := s.applyUpdate(&user, input)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.store.UpdateUser(ctx, user, passwordHash); err != nil {
		return domain.User{}, err
	}
	if sensitive {
		if err := s.store.DeleteSessionsForUser(ctx, user.ID); err != nil {
			return domain.User{}, err
		}
	}
	return user, nil
}

func (s *Service) UpdateProfile(ctx context.Context, actor domain.User, input UpdateProfileInput) (domain.User, bool, error) {
	user, err := s.store.FindUserByID(ctx, actor.ID)
	if err != nil {
		return domain.User{}, false, err
	}
	if input.DisplayName != nil {
		user.DisplayName = cleanDisplayName(*input.DisplayName, user.Username)
	}
	if input.Email != nil {
		user.Email, err = cleanEmail(*input.Email)
		if err != nil {
			return domain.User{}, false, err
		}
	}
	var passwordHash *string
	passwordChanged := strings.TrimSpace(input.NewPassword) != ""
	if passwordChanged {
		if user.AuthSource == "oidc" {
			return domain.User{}, false, fmt.Errorf("%w: password is managed by the OIDC provider", ErrInvalidInput)
		}
		_, currentHash, lookupErr := s.store.FindUserCredentials(ctx, strings.ToLower(user.Username))
		if lookupErr != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(input.CurrentPassword)) != nil {
			return domain.User{}, false, fmt.Errorf("%w: current password is incorrect", ErrInvalidInput)
		}
		if err := validatePassword(input.NewPassword); err != nil {
			return domain.User{}, false, err
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(input.NewPassword), s.bcryptCost)
		if hashErr != nil {
			return domain.User{}, false, hashErr
		}
		value := string(hash)
		passwordHash = &value
	}
	user.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.UpdateUser(ctx, user, passwordHash); err != nil {
		return domain.User{}, false, err
	}
	if passwordChanged {
		if err := s.store.DeleteSessionsForUser(ctx, user.ID); err != nil {
			return domain.User{}, false, err
		}
	}
	return user, passwordChanged, nil
}

func (s *Service) DeleteUser(ctx context.Context, actor domain.User, id string) error {
	if actor.Role != domain.RoleAdmin {
		return ErrForbidden
	}
	if actor.ID == id {
		return ErrSelfDelete
	}
	return s.store.DeleteUser(ctx, id)
}

func (s *Service) applyUpdate(user *domain.User, input UpdateUserInput) (*string, bool, error) {
	sensitive := false
	if input.DisplayName != nil {
		user.DisplayName = cleanDisplayName(*input.DisplayName, user.Username)
	}
	if input.Email != nil {
		email, err := cleanEmail(*input.Email)
		if err != nil {
			return nil, false, err
		}
		user.Email = email
	}
	if input.Role != nil {
		if err := validateRole(*input.Role); err != nil {
			return nil, false, err
		}
		sensitive = sensitive || user.Role != *input.Role
		user.Role = *input.Role
	}
	if input.Namespaces != nil {
		next := cleanNamespaces(*input.Namespaces)
		sensitive = sensitive || strings.Join(user.Namespaces, "\x00") != strings.Join(next, "\x00")
		user.Namespaces = next
	}
	if input.Disabled != nil {
		sensitive = sensitive || user.Disabled != *input.Disabled
		user.Disabled = *input.Disabled
	}
	var passwordHash *string
	if input.Password != nil && *input.Password != "" {
		if err := validatePassword(*input.Password); err != nil {
			return nil, false, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*input.Password), s.bcryptCost)
		if err != nil {
			return nil, false, err
		}
		value := string(hash)
		passwordHash = &value
		sensitive = true
	}
	user.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return passwordHash, sensitive, nil
}

func cleanNamespaces(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func validateUsername(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if !usernamePattern.MatchString(value) {
		return "", "", fmt.Errorf("%w: username must be 3-64 characters and contain only letters, numbers, dot, underscore, or hyphen", ErrInvalidInput)
	}
	return value, strings.ToLower(value), nil
}

func validatePassword(value string) error {
	if len(value) < 8 || len(value) > 128 {
		return fmt.Errorf("%w: password must be between 8 and 128 characters", ErrInvalidInput)
	}
	return nil
}

func validateRole(role domain.UserRole) error {
	if role != domain.RoleAdmin && role != domain.RoleViewer {
		return fmt.Errorf("%w: role must be admin or viewer", ErrInvalidInput)
	}
	return nil
}

func cleanDisplayName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len([]rune(value)) > 100 {
		return string([]rune(value)[:100])
	}
	return value
}

func cleanEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	address, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(address.Address, value) {
		return "", fmt.Errorf("%w: email is invalid", ErrInvalidInput)
	}
	return value, nil
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func newID() string {
	value, err := randomToken(18)
	if err != nil {
		return fmt.Sprintf("user-%d", time.Now().UnixNano())
	}
	return value
}
