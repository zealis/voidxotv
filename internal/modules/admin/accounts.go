package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── 后台账号（§4.7 账号与权限）────────────────────────────────────

// AdminRecord 是账号的运维视角，永远不含任何口令材料。
type AdminRecord struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (r *Repository) ListAdmins(ctx context.Context) ([]AdminRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, email, role, status, created_at, updated_at
		FROM admin_users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("admin: list admins: %w", err)
	}
	defer rows.Close()
	result := []AdminRecord{}
	for rows.Next() {
		var item AdminRecord
		var created, updated time.Time
		if err := rows.Scan(&item.ID, &item.Email, &item.Role, &item.Status,
			&created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt = created.UTC().Format(time.RFC3339)
		item.UpdatedAt = updated.UTC().Format(time.RFC3339)
		result = append(result, item)
	}
	return result, rows.Err()
}

// FindAdminByID 供改密与权限变更使用；返回值含口令哈希，只允许服务端内部使用。
func (r *Repository) FindAdminByID(ctx context.Context, id int64) (AdminAccount, error) {
	var account AdminAccount
	err := r.db.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status
		FROM admin_users WHERE id = $1`, id).Scan(
		&account.ID, &account.Email, &account.PasswordHash, &account.Role, &account.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return account, ErrNotFound
	}
	if err != nil {
		return account, fmt.Errorf("admin: find admin: %w", err)
	}
	return account, nil
}

func (r *Repository) CreateAdmin(ctx context.Context, email, role, passwordHash string) (AdminRecord, error) {
	var item AdminRecord
	var created, updated time.Time
	err := r.db.QueryRow(ctx, `
		INSERT INTO admin_users (email, password_hash, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', now(), now())
		RETURNING id, email, role, status, created_at, updated_at`,
		email, passwordHash, role).Scan(
		&item.ID, &item.Email, &item.Role, &item.Status, &created, &updated)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return item, fmt.Errorf("%w: 该邮箱已存在", ErrConflict)
	}
	if err != nil {
		return item, fmt.Errorf("admin: create admin: %w", err)
	}
	item.CreatedAt = created.UTC().Format(time.RFC3339)
	item.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return item, nil
}

func (r *Repository) UpdateAdminRole(ctx context.Context, id int64, role string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE admin_users SET role = $2, updated_at = now() WHERE id = $1`, id, role)
	if err != nil {
		return fmt.Errorf("admin: update admin role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) UpdateAdminStatus(ctx context.Context, id int64, status string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE admin_users SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("admin: update admin status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) UpdateAdminPassword(ctx context.Context, id int64, passwordHash string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE admin_users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return fmt.Errorf("admin: update admin password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountActiveSuperAdmins 统计除 excludeID 之外仍可用的超管数量，
// 用于阻止「把最后一个超管降级/停用」导致后台锁死。
func (r *Repository) CountActiveSuperAdmins(ctx context.Context, excludeID int64) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM admin_users
		WHERE role = 'super_admin' AND status = 'active' AND id <> $1`, excludeID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("admin: count super admins: %w", err)
	}
	return count, nil
}
