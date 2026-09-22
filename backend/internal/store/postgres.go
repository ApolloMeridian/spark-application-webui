package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

type AuditStore interface {
	Insert(context.Context, domain.OperationAudit) error
	List(context.Context, int) ([]domain.OperationAudit, error)
	UpsertApplications(context.Context, []domain.SparkApplication) error
	HistorySummary(context.Context, time.Time, time.Time) (domain.HistorySummary, error)
	Ping(context.Context) error
	Close()
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string, maxConnections int32) (*PostgresStore, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = maxConnections
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	statements := []string{`
CREATE TABLE IF NOT EXISTS operation_audit (
  id text PRIMARY KEY,
  application_name text NOT NULL,
  namespace text NOT NULL,
  operator_name text NOT NULL,
  operation text NOT NULL,
  reason text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  result text NOT NULL CHECK (result IN ('SUCCESS', 'FAILED')),
	  message text NOT NULL
	)`,
		`CREATE INDEX IF NOT EXISTS operation_audit_occurred_at_idx ON operation_audit (occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS operation_audit_application_idx ON operation_audit (namespace, application_name, occurred_at DESC)`,
		`CREATE TABLE IF NOT EXISTS spark_application_history (
  application_id text PRIMARY KEY,
  cluster_name text NOT NULL,
  namespace text NOT NULL,
  application_name text NOT NULL,
  owner_name text NOT NULL DEFAULT '',
  state text NOT NULL,
  created_at timestamptz NOT NULL,
  started_at timestamptz,
  finished_at timestamptz,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
)`,
		`CREATE INDEX IF NOT EXISTS spark_application_history_created_at_idx ON spark_application_history (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS spark_application_history_finished_at_idx ON spark_application_history (finished_at DESC)`,
		`CREATE TABLE IF NOT EXISTS local_users (
  id text PRIMARY KEY,
  username text NOT NULL,
  username_normalized text NOT NULL UNIQUE,
  display_name text NOT NULL,
  email text NOT NULL DEFAULT '',
  role text NOT NULL CHECK (role IN ('admin', 'viewer')),
  auth_source text NOT NULL DEFAULT 'local',
  oidc_issuer text,
  oidc_subject text,
  password_hash text,
  disabled boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
)`,
		`ALTER TABLE local_users ADD COLUMN IF NOT EXISTS auth_source text NOT NULL DEFAULT 'local'`,
		`ALTER TABLE local_users ADD COLUMN IF NOT EXISTS oidc_issuer text`,
		`ALTER TABLE local_users ADD COLUMN IF NOT EXISTS oidc_subject text`,
		`ALTER TABLE local_users ALTER COLUMN password_hash DROP NOT NULL`,
		`CREATE INDEX IF NOT EXISTS local_users_role_idx ON local_users (role, disabled)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS local_users_oidc_identity_idx ON local_users (oidc_issuer, oidc_subject) WHERE oidc_subject IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS local_user_sessions (
  token_hash text PRIMARY KEY,
  user_id text NOT NULL REFERENCES local_users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
)`,
		`CREATE INDEX IF NOT EXISTS local_user_sessions_user_idx ON local_user_sessions (user_id)`,
		`CREATE INDEX IF NOT EXISTS local_user_sessions_expiry_idx ON local_user_sessions (expires_at)`,
		`CREATE TABLE IF NOT EXISTS oidc_login_states (
  state_hash text PRIMARY KEY,
  nonce text NOT NULL,
  code_verifier text NOT NULL,
  redirect_url text NOT NULL,
  return_url text NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
)`,
		`CREATE INDEX IF NOT EXISTS oidc_login_states_expiry_idx ON oidc_login_states (expires_at)`,
		`CREATE TABLE IF NOT EXISTS application_settings (
  setting_key text PRIMARY KEY,
  setting_value text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
)`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate audit schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) Insert(ctx context.Context, audit domain.OperationAudit) error {
	when, err := time.Parse(time.RFC3339Nano, audit.Timestamp)
	if err != nil {
		return fmt.Errorf("parse audit timestamp: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO operation_audit
  (id, application_name, namespace, operator_name, operation, reason, occurred_at, result, message)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		audit.ID, audit.ApplicationName, audit.Namespace, audit.Operator, audit.Operation,
		audit.Reason, when, audit.Result, audit.Message)
	if err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, limit int) ([]domain.OperationAudit, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, application_name, namespace, operator_name, operation, reason, occurred_at, result, message
FROM operation_audit ORDER BY occurred_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit records: %w", err)
	}
	defer rows.Close()
	result := make([]domain.OperationAudit, 0)
	for rows.Next() {
		var audit domain.OperationAudit
		var when time.Time
		if err := rows.Scan(&audit.ID, &audit.ApplicationName, &audit.Namespace, &audit.Operator,
			&audit.Operation, &audit.Reason, &when, &audit.Result, &audit.Message); err != nil {
			return nil, fmt.Errorf("scan audit record: %w", err)
		}
		audit.Timestamp = when.UTC().Format(time.RFC3339Nano)
		result = append(result, audit)
	}
	return result, rows.Err()
}

func (s *PostgresStore) UpsertApplications(ctx context.Context, applications []domain.SparkApplication) error {
	if len(applications) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin application history transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, app := range applications {
		createdAt, err := time.Parse(time.RFC3339, app.CreatedAt)
		if err != nil {
			return fmt.Errorf("parse created time for %s/%s: %w", app.Namespace, app.Name, err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO spark_application_history
  (application_id, cluster_name, namespace, application_name, owner_name, state, created_at, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (application_id) DO UPDATE SET
  cluster_name=EXCLUDED.cluster_name,
  namespace=EXCLUDED.namespace,
  application_name=EXCLUDED.application_name,
  owner_name=EXCLUDED.owner_name,
  state=EXCLUDED.state,
  created_at=EXCLUDED.created_at,
  started_at=COALESCE(EXCLUDED.started_at, spark_application_history.started_at),
  finished_at=COALESCE(EXCLUDED.finished_at, spark_application_history.finished_at),
  last_seen_at=now()`,
			app.ID, app.Cluster, app.Namespace, app.Name, app.Owner, app.State, createdAt,
			optionalTime(app.StartedAt), optionalTime(app.FinishedAt))
		if err != nil {
			return fmt.Errorf("upsert application history for %s/%s: %w", app.Namespace, app.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit application history transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) HistorySummary(ctx context.Context, from, to time.Time) (domain.HistorySummary, error) {
	result := domain.HistorySummary{From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339)}
	err := s.pool.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE created_at >= $1 AND created_at <= $2),
  count(*) FILTER (
    WHERE state IN ('FAILED', 'SUBMISSION_FAILED')
      AND COALESCE(finished_at, last_seen_at) >= $1
      AND COALESCE(finished_at, last_seen_at) <= $2
  )
FROM spark_application_history`, from, to).Scan(&result.Submitted, &result.Failed)
	if err != nil {
		return domain.HistorySummary{}, fmt.Errorf("query application history summary: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) BootstrapInitialAdmin(ctx context.Context, user domain.User, normalized, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin initial administrator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('spark-control-center-local-auth'))`); err != nil {
		return err
	}
	var bootstrapped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM application_settings WHERE setting_key='local_auth_bootstrapped')`).Scan(&bootstrapped); err != nil {
		return fmt.Errorf("read local authentication bootstrap state: %w", err)
	}
	if bootstrapped {
		return tx.Commit(ctx)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, user.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO local_users (id, username, username_normalized, display_name, email, role, auth_source, password_hash, disabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,'local',$7,false,$8,$8)`, user.ID, user.Username, normalized, user.DisplayName, user.Email, user.Role, passwordHash, createdAt)
	if isUniqueViolation(err) {
		return localauth.ErrUsernameExists
	}
	if err != nil {
		return fmt.Errorf("create initial administrator: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO application_settings (setting_key, setting_value) VALUES ('local_auth_bootstrapped', 'true')`); err != nil {
		return fmt.Errorf("save local authentication bootstrap state: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) FindUserCredentials(ctx context.Context, normalized string) (domain.User, string, error) {
	var user domain.User
	var passwordHash string
	var createdAt, updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
SELECT id, username, display_name, email, role, auth_source, disabled, created_at, updated_at, password_hash
FROM local_users WHERE username_normalized=$1 AND auth_source='local'`, normalized).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Role, &user.AuthSource, &user.Disabled, &createdAt, &updatedAt, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", localauth.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("query user credentials: %w", err)
	}
	setUserTimes(&user, createdAt, updatedAt)
	return user, passwordHash, nil
}

func (s *PostgresStore) FindUserByID(ctx context.Context, id string) (domain.User, error) {
	var user domain.User
	var createdAt, updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
SELECT id, username, display_name, email, role, auth_source, disabled, created_at, updated_at
FROM local_users WHERE id=$1`, id).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Role, &user.AuthSource, &user.Disabled, &createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, localauth.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query user: %w", err)
	}
	setUserTimes(&user, createdAt, updatedAt)
	return user, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, username, display_name, email, role, auth_source, disabled, created_at, updated_at
FROM local_users ORDER BY created_at ASC, username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]domain.User, 0)
	for rows.Next() {
		var user domain.User
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Role, &user.AuthSource, &user.Disabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		setUserTimes(&user, createdAt, updatedAt)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *PostgresStore) CreateUser(ctx context.Context, user domain.User, normalized, passwordHash string) error {
	createdAt, err := time.Parse(time.RFC3339Nano, user.CreatedAt)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO local_users (id, username, username_normalized, display_name, email, role, auth_source, password_hash, disabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,'local',$7,$8,$9,$9)`, user.ID, user.Username, normalized, user.DisplayName, user.Email, user.Role, passwordHash, user.Disabled, createdAt)
	if isUniqueViolation(err) {
		return localauth.ErrUsernameExists
	}
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, user domain.User, passwordHash *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('spark-control-center-local-auth'))`); err != nil {
		return err
	}
	var currentRole domain.UserRole
	var currentDisabled bool
	var currentAuthSource string
	if err := tx.QueryRow(ctx, `SELECT role, disabled, auth_source FROM local_users WHERE id=$1 FOR UPDATE`, user.ID).Scan(&currentRole, &currentDisabled, &currentAuthSource); errors.Is(err, pgx.ErrNoRows) {
		return localauth.ErrUserNotFound
	} else if err != nil {
		return err
	}
	if currentAuthSource == "local" && currentRole == domain.RoleAdmin && !currentDisabled && (user.Role != domain.RoleAdmin || user.Disabled) {
		var activeAdmins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM local_users WHERE role='admin' AND auth_source='local' AND disabled=false`).Scan(&activeAdmins); err != nil {
			return err
		}
		if activeAdmins <= 1 {
			return localauth.ErrLastAdmin
		}
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, user.UpdatedAt)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
UPDATE local_users SET display_name=$2, email=$3, role=$4, disabled=$5, updated_at=$6,
password_hash=COALESCE($7, password_hash) WHERE id=$1`, user.ID, user.DisplayName, user.Email, user.Role, user.Disabled, updatedAt, passwordHash)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if command.RowsAffected() == 0 {
		return localauth.ErrUserNotFound
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('spark-control-center-local-auth'))`); err != nil {
		return err
	}
	var role domain.UserRole
	var disabled bool
	var authSource string
	if err := tx.QueryRow(ctx, `SELECT role, disabled, auth_source FROM local_users WHERE id=$1 FOR UPDATE`, id).Scan(&role, &disabled, &authSource); errors.Is(err, pgx.ErrNoRows) {
		return localauth.ErrUserNotFound
	} else if err != nil {
		return err
	}
	if authSource == "local" && role == domain.RoleAdmin && !disabled {
		var activeAdmins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM local_users WHERE role='admin' AND auth_source='local' AND disabled=false`).Scan(&activeAdmins); err != nil {
			return err
		}
		if activeAdmins <= 1 {
			return localauth.ErrLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM local_users WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) CreateSession(ctx context.Context, session domain.UserSession) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM local_user_sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO local_user_sessions (token_hash, user_id, expires_at) VALUES ($1,$2,$3)`, session.TokenHash, session.UserID, session.ExpiresAt); err != nil {
		return fmt.Errorf("create user session: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) FindSession(ctx context.Context, tokenHash string, now time.Time) (domain.User, domain.UserSession, error) {
	var user domain.User
	var session domain.UserSession
	var createdAt, updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
SELECT u.id, u.username, u.display_name, u.email, u.role, u.auth_source, u.disabled, u.created_at, u.updated_at,
       s.token_hash, s.user_id, s.expires_at
FROM local_user_sessions s JOIN local_users u ON u.id=s.user_id
WHERE s.token_hash=$1 AND s.expires_at>$2`, tokenHash, now).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Role, &user.AuthSource, &user.Disabled, &createdAt, &updatedAt,
		&session.TokenHash, &session.UserID, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.UserSession{}, localauth.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, domain.UserSession{}, fmt.Errorf("query user session: %w", err)
	}
	setUserTimes(&user, createdAt, updatedAt)
	_, _ = s.pool.Exec(ctx, `UPDATE local_user_sessions SET last_seen_at=now() WHERE token_hash=$1`, tokenHash)
	return user, session, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM local_user_sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *PostgresStore) DeleteSessionsForUser(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM local_user_sessions WHERE user_id=$1`, userID)
	return err
}

func (s *PostgresStore) UpsertOIDCUser(ctx context.Context, candidate domain.User, normalized, issuer, subject string, autoCreate bool) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('spark-control-center-local-auth'))`); err != nil {
		return domain.User{}, err
	}
	var user domain.User
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `
SELECT id, username, display_name, email, role, auth_source, disabled, created_at, updated_at
FROM local_users WHERE oidc_issuer=$1 AND oidc_subject=$2 FOR UPDATE`, issuer, subject).Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Role, &user.AuthSource, &user.Disabled, &createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if !autoCreate {
			return domain.User{}, localauth.ErrUserNotFound
		}
		createdAt, err = time.Parse(time.RFC3339Nano, candidate.CreatedAt)
		if err != nil {
			return domain.User{}, err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO local_users (id, username, username_normalized, display_name, email, role, auth_source, oidc_issuer, oidc_subject, password_hash, disabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,'oidc',$7,$8,NULL,false,$9,$9)`, candidate.ID, candidate.Username, normalized, candidate.DisplayName, candidate.Email, candidate.Role, issuer, subject, createdAt)
		if isUniqueViolation(err) {
			return domain.User{}, localauth.ErrUsernameExists
		}
		if err != nil {
			return domain.User{}, fmt.Errorf("create OIDC user: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, err
		}
		return candidate, nil
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query OIDC user: %w", err)
	}
	setUserTimes(&user, createdAt, updatedAt)
	user.Username = candidate.Username
	user.DisplayName = candidate.DisplayName
	user.Email = candidate.Email
	user.Role = candidate.Role
	user.AuthSource = "oidc"
	user.UpdatedAt = candidate.UpdatedAt
	updatedAt, err = time.Parse(time.RFC3339Nano, candidate.UpdatedAt)
	if err != nil {
		return domain.User{}, err
	}
	_, err = tx.Exec(ctx, `
UPDATE local_users SET username=$2, username_normalized=$3, display_name=$4, email=$5, role=$6,
auth_source='oidc', updated_at=$7 WHERE id=$1`, user.ID, user.Username, normalized, user.DisplayName, user.Email, user.Role, updatedAt)
	if isUniqueViolation(err) {
		return domain.User{}, localauth.ErrUsernameExists
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("update OIDC user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *PostgresStore) SaveOIDCLoginState(ctx context.Context, state domain.OIDCLoginState) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM oidc_login_states WHERE expires_at <= now()`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO oidc_login_states (state_hash, nonce, code_verifier, redirect_url, return_url, expires_at)
VALUES ($1,$2,$3,$4,$5,$6)`, state.StateHash, state.Nonce, state.CodeVerifier, state.RedirectURL, state.ReturnURL, state.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save OIDC login state: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ConsumeOIDCLoginState(ctx context.Context, stateHash string, now time.Time) (domain.OIDCLoginState, error) {
	var state domain.OIDCLoginState
	err := s.pool.QueryRow(ctx, `
DELETE FROM oidc_login_states WHERE state_hash=$1 AND expires_at>$2
RETURNING state_hash, nonce, code_verifier, redirect_url, return_url, expires_at`, stateHash, now).Scan(
		&state.StateHash, &state.Nonce, &state.CodeVerifier, &state.RedirectURL, &state.ReturnURL, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OIDCLoginState{}, localauth.ErrUnauthorized
	}
	if err != nil {
		return domain.OIDCLoginState{}, fmt.Errorf("consume OIDC login state: %w", err)
	}
	return state, nil
}

func setUserTimes(user *domain.User, createdAt, updatedAt time.Time) {
	user.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	user.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func optionalTime(value string) any {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return parsed
}

func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *PostgresStore) Close()                         { s.pool.Close() }
