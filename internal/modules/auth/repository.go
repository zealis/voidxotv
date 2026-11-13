package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrUserExists   = errors.New("user already exists")
)

// Repository owns the users and long-term session records in PostgreSQL.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func (r *Repository) CreateUser(ctx context.Context, handle, email, passwordHash string) (User, error) {
	const query = `
		INSERT INTO users (handle, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, handle, COALESCE(email, ''), password_hash, role, status, created_at`
	var user User
	err := r.db.QueryRow(ctx, query, handle, nullable(email), passwordHash).Scan(
		&user.ID, &user.Handle, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUserExists
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func (r *Repository) FindByHandle(ctx context.Context, handle string) (User, error) {
	const query = `
		SELECT id, handle, COALESCE(email, ''), password_hash, role, status, created_at
		FROM users WHERE handle = $1`
	return r.scanUser(r.db.QueryRow(ctx, query, handle))
}

func (r *Repository) FindByID(ctx context.Context, id int64) (User, error) {
	const query = `
		SELECT id, handle, COALESCE(email, ''), password_hash, role, status, created_at
		FROM users WHERE id = $1`
	return r.scanUser(r.db.QueryRow(ctx, query, id))
}

func (r *Repository) scanUser(row pgx.Row) (User, error) {
	var user User
	if err := row.Scan(&user.ID, &user.Handle, &user.Email, &user.PasswordHash,
		&user.Role, &user.Status, &user.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return user, nil
}

// RecordSession keeps only the token hash for device management and audit.
func (r *Repository) RecordSession(ctx context.Context, userID int64, tokenHash []byte,
	deviceID, ipAddress, userAgent string, expiresAt time.Time) error {
	const query = `
		INSERT INTO user_sessions (user_id, token_hash, device_id, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5, $6)`
	_, err := r.db.Exec(ctx, query, userID, tokenHash, deviceID, ipAddress, userAgent, expiresAt)
	if err != nil {
		return fmt.Errorf("record session: %w", err)
	}
	return nil
}

func (r *Repository) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.Exec(ctx, `UPDATE user_sessions SET revoked_at = now() WHERE token_hash = $1`, tokenHash)
	return err
}

func (r *Repository) RevokeAllSessions(ctx context.Context, userID int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// SetDefaultAvatarKey 给新用户写默认头像 key。带 avatar_key IS NULL 条件：
// 注册后的异步任务可能晚于用户自己上传头像完成，不能把用户的选择覆盖掉。
// 返回是否真的写入，false 时调用方需要删掉已上传但无人引用的对象。
func (r *Repository) SetDefaultAvatarKey(ctx context.Context, userID int64, key string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE users SET avatar_key = $2, updated_at = now()
		WHERE id = $1 AND avatar_key IS NULL`, userID, key)
	if err != nil {
		return false, fmt.Errorf("auth: set default avatar: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
