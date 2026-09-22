package localauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"spark-control-center/backend/internal/domain"
)

type memoryStore struct {
	users    map[string]domain.User
	hashes   map[string]string
	sessions map[string]domain.UserSession
}

func newMemoryStore(t *testing.T, users ...domain.User) *memoryStore {
	t.Helper()
	store := &memoryStore{users: map[string]domain.User{}, hashes: map[string]string{}, sessions: map[string]domain.UserSession{}}
	for _, user := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		store.users[user.ID] = user
		store.hashes[strings.ToLower(user.Username)] = string(hash)
	}
	return store
}

func (m *memoryStore) BootstrapInitialAdmin(context.Context, domain.User, string, string) error {
	return nil
}
func (m *memoryStore) FindUserCredentials(_ context.Context, normalized string) (domain.User, string, error) {
	for _, user := range m.users {
		if strings.EqualFold(user.Username, normalized) {
			return user, m.hashes[normalized], nil
		}
	}
	return domain.User{}, "", ErrUserNotFound
}
func (m *memoryStore) FindUserByID(_ context.Context, id string) (domain.User, error) {
	user, ok := m.users[id]
	if !ok {
		return domain.User{}, ErrUserNotFound
	}
	return user, nil
}
func (m *memoryStore) ListUsers(context.Context) ([]domain.User, error) {
	result := make([]domain.User, 0, len(m.users))
	for _, user := range m.users {
		result = append(result, user)
	}
	return result, nil
}
func (m *memoryStore) CreateUser(_ context.Context, user domain.User, normalized, hash string) error {
	for _, current := range m.users {
		if strings.EqualFold(current.Username, user.Username) {
			return ErrUsernameExists
		}
	}
	m.users[user.ID] = user
	m.hashes[normalized] = hash
	return nil
}
func (m *memoryStore) UpsertOIDCUser(_ context.Context, user domain.User, normalized, _, _ string, autoCreate bool) (domain.User, error) {
	for id, current := range m.users {
		if strings.EqualFold(current.Username, normalized) {
			user.ID = id
			user.CreatedAt = current.CreatedAt
			m.users[id] = user
			return user, nil
		}
	}
	if !autoCreate {
		return domain.User{}, ErrUserNotFound
	}
	m.users[user.ID] = user
	return user, nil
}
func (m *memoryStore) UpdateUser(_ context.Context, user domain.User, hash *string) error {
	m.users[user.ID] = user
	if hash != nil {
		m.hashes[strings.ToLower(user.Username)] = *hash
	}
	return nil
}
func (m *memoryStore) DeleteUser(_ context.Context, id string) error { delete(m.users, id); return nil }
func (m *memoryStore) CreateSession(_ context.Context, session domain.UserSession) error {
	m.sessions[session.TokenHash] = session
	return nil
}
func (m *memoryStore) FindSession(_ context.Context, hash string, now time.Time) (domain.User, domain.UserSession, error) {
	session, ok := m.sessions[hash]
	if !ok || !session.ExpiresAt.After(now) {
		return domain.User{}, domain.UserSession{}, ErrUserNotFound
	}
	return m.users[session.UserID], session, nil
}
func (m *memoryStore) DeleteSession(_ context.Context, hash string) error {
	delete(m.sessions, hash)
	return nil
}
func (m *memoryStore) DeleteSessionsForUser(_ context.Context, id string) error {
	for hash, session := range m.sessions {
		if session.UserID == id {
			delete(m.sessions, hash)
		}
	}
	return nil
}

func TestLoginAndAuthenticate(t *testing.T) {
	admin := domain.User{ID: "admin-1", Username: "Admin", Role: domain.RoleAdmin}
	store := newMemoryStore(t, admin)
	service := New(store, time.Hour, bcrypt.MinCost)
	user, token, expiresAt, err := service.Login(context.Background(), "admin", "password123")
	if err != nil || user.ID != admin.ID || token == "" || !expiresAt.After(time.Now()) {
		t.Fatalf("unexpected login: user=%#v token=%q expires=%v err=%v", user, token, expiresAt, err)
	}
	authenticated, _, err := service.Authenticate(context.Background(), token)
	if err != nil || authenticated.ID != admin.ID {
		t.Fatalf("unexpected session: user=%#v err=%v", authenticated, err)
	}
	if _, _, _, err := service.Login(context.Background(), "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestViewerCannotManageUsersAndAdminCanCreateAdmin(t *testing.T) {
	admin := domain.User{ID: "admin-1", Username: "admin", Role: domain.RoleAdmin}
	viewer := domain.User{ID: "viewer-1", Username: "viewer", Role: domain.RoleViewer}
	store := newMemoryStore(t, admin, viewer)
	service := New(store, time.Hour, bcrypt.MinCost)
	if _, err := service.CreateUser(context.Background(), viewer, CreateUserInput{Username: "blocked", Role: domain.RoleViewer, Password: "password123"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	created, err := service.CreateUser(context.Background(), admin, CreateUserInput{Username: "second-admin", DisplayName: "Second Admin", Role: domain.RoleAdmin, Password: "another-password"})
	if err != nil || created.Role != domain.RoleAdmin {
		t.Fatalf("unexpected created user: %#v err=%v", created, err)
	}
	if store.hashes["second-admin"] == "another-password" {
		t.Fatal("password was stored in plaintext")
	}
}

func TestProfilePasswordChangeInvalidatesSessions(t *testing.T) {
	viewer := domain.User{ID: "viewer-1", Username: "viewer", Role: domain.RoleViewer}
	store := newMemoryStore(t, viewer)
	service := New(store, time.Hour, bcrypt.MinCost)
	_, token, _, err := service.Login(context.Background(), "viewer", "password123")
	if err != nil {
		t.Fatal(err)
	}
	name := "Viewer One"
	updated, changed, err := service.UpdateProfile(context.Background(), viewer, UpdateProfileInput{DisplayName: &name, CurrentPassword: "password123", NewPassword: "new-password-123"})
	if err != nil || !changed || updated.DisplayName != name {
		t.Fatalf("unexpected update: %#v changed=%v err=%v", updated, changed, err)
	}
	if _, _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session remained valid: %v", err)
	}
	if _, _, _, err := service.Login(context.Background(), "viewer", "new-password-123"); err != nil {
		t.Fatalf("new password did not work: %v", err)
	}
}
