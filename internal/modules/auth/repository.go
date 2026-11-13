package auth

import (
	"context"
	"errors"
	"fmt"
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

// CreateUser 用 CTE 先取序列值再插入：夜街号就是用户 ID，所以 handle 与
// display_name（「用户 + 夜街号」）必须在 INSERT 时就能拿到新 ID，
// 否则要先插临时 handle 再回写，多一次往返还可能撞唯一索引。
func (r *Repository) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	const query = `
		WITH new_id AS (SELECT nextval('users_id_seq') AS id)
		INSERT INTO users (id, handle, email, password_hash, display_name)
		OVERRIDING SYSTEM VALUE
		SELECT id, id::text, $1, $2, '用户' || id FROM new_id
		RETURNING id, handle, COALESCE(email, ''), password_hash, role, status, created_at`
	var user User
	err := r.db.QueryRow(ctx, query, email, passwordHash).Scan(
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

// FindByAccount 按邮箱找账号；邮箱不匹配时回落到 handle（纯数字夜街号），
// 兼容后台脚本、curl 与旧客户端。已登录用户的 email 为 NULL 时只可能走 handle。
func (r *Repository) FindByAccount(ctx context.Context, account string) (User, error) {
	const query = `
		SELECT id, handle, COALESCE(email, ''), password_hash, role, status, created_at
		FROM users WHERE email = $1 OR handle = $1
		ORDER BY (email = $1) DESC
		LIMIT 1`
	return r.scanUser(r.db.QueryRow(ctx, query, account))
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
