package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Load 返回用户设置；没有记录时回落到默认值（迁移的列默认值与此一致）。
func (r *Repository) Load(ctx context.Context, userID int64) (Settings, error) {
	var value Settings
	err := r.db.QueryRow(ctx, `
		SELECT notify_interaction, notify_follow, notify_system,
		       quiet_start, quiet_end, default_visibility, autoplay, reduce_motion, theme, updated_at
		FROM user_settings WHERE user_id = $1`, userID).Scan(
		&value.NotifyInteraction, &value.NotifyFollow, &value.NotifySystem,
		&value.QuietStart, &value.QuietEnd, &value.DefaultVisibility,
		&value.Autoplay, &value.ReduceMotion, &value.Theme, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Default(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("settings: load: %w", err)
	}
	return value, nil
}

// Update 做「缺省即不改」的部分更新。COALESCE 需要显式类型，
// 否则 pgx 以 unknown 传参时 Postgres 无法推断列类型。
func (r *Repository) Update(ctx context.Context, userID int64, patch Patch) (Settings, error) {
	var value Settings
	err := r.db.QueryRow(ctx, `
		INSERT INTO user_settings (
		  user_id, notify_interaction, notify_follow, notify_system,
		  quiet_start, quiet_end, default_visibility, autoplay, reduce_motion, theme, updated_at)
		VALUES ($1,
		  COALESCE($2::boolean, TRUE), COALESCE($3::boolean, TRUE), COALESCE($4::boolean, TRUE),
		  COALESCE($5::smallint, 0), COALESCE($6::smallint, 0), COALESCE($7::text, 'public'),
		  COALESCE($8::boolean, TRUE), COALESCE($9::boolean, FALSE), COALESCE($10::text, 'system'), now())
		ON CONFLICT (user_id) DO UPDATE SET
		  notify_interaction = COALESCE($2::boolean, user_settings.notify_interaction),
		  notify_follow      = COALESCE($3::boolean, user_settings.notify_follow),
		  notify_system      = COALESCE($4::boolean, user_settings.notify_system),
		  quiet_start        = COALESCE($5::smallint, user_settings.quiet_start),
		  quiet_end          = COALESCE($6::smallint, user_settings.quiet_end),
		  default_visibility = COALESCE($7::text, user_settings.default_visibility),
		  autoplay           = COALESCE($8::boolean, user_settings.autoplay),
		  reduce_motion      = COALESCE($9::boolean, user_settings.reduce_motion),
		  theme              = COALESCE($10::text, user_settings.theme),
		  updated_at         = now()
		RETURNING notify_interaction, notify_follow, notify_system,
		          quiet_start, quiet_end, default_visibility, autoplay, reduce_motion, theme, updated_at`,
		userID, patch.NotifyInteraction, patch.NotifyFollow, patch.NotifySystem,
		patch.QuietStart, patch.QuietEnd, patch.DefaultVisibility,
		patch.Autoplay, patch.ReduceMotion, patch.Theme).Scan(
		&value.NotifyInteraction, &value.NotifyFollow, &value.NotifySystem,
		&value.QuietStart, &value.QuietEnd, &value.DefaultVisibility,
		&value.Autoplay, &value.ReduceMotion, &value.Theme, &value.UpdatedAt)
	if err != nil {
		return Settings{}, fmt.Errorf("settings: update: %w", err)
	}
	return value, nil
}

func (r *Repository) Profile(ctx context.Context, userID int64) (Profile, error) {
	var profile Profile
	err := r.db.QueryRow(ctx, `
		SELECT handle, display_name, bio, email, COALESCE(avatar_key, ''), created_at
		FROM users WHERE id = $1`,
		userID).Scan(&profile.Handle, &profile.DisplayName, &profile.Bio,
		&profile.Email, &profile.AvatarKey, &profile.CreatedAt)
	if err != nil {
		return Profile{}, fmt.Errorf("settings: profile: %w", err)
	}
	return profile, nil
}

func (r *Repository) UpdateProfile(ctx context.Context, userID int64, displayName, bio *string) (Profile, error) {
	var profile Profile
	err := r.db.QueryRow(ctx, `
		UPDATE users
		SET display_name = COALESCE($2::text, display_name),
		    bio = COALESCE($3::text, bio),
		    updated_at = now()
		WHERE id = $1
		RETURNING handle, display_name, bio, email, COALESCE(avatar_key, ''), created_at`,
		userID, displayName, bio).Scan(&profile.Handle, &profile.DisplayName,
		&profile.Bio, &profile.Email, &profile.AvatarKey, &profile.CreatedAt)
	if err != nil {
		return Profile{}, fmt.Errorf("settings: update profile: %w", err)
	}
	return profile, nil
}

// AvatarKey 返回当前头像对象键；空字符串表示未设置。
func (r *Repository) AvatarKey(ctx context.Context, userID int64) (string, error) {
	var key string
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(avatar_key, '') FROM users WHERE id = $1`, userID).Scan(&key); err != nil {
		return "", fmt.Errorf("settings: avatar key: %w", err)
	}
	return key, nil
}

// SetAvatar 写入或清空头像键（空串表示恢复默认头像）。
func (r *Repository) SetAvatar(ctx context.Context, userID int64, key string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE users SET avatar_key = NULLIF($2, ''), updated_at = now() WHERE id = $1`,
		userID, key)
	if err != nil {
		return fmt.Errorf("settings: set avatar: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *Repository) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	if err := r.db.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		return "", fmt.Errorf("settings: password hash: %w", err)
	}
	return hash, nil
}

func (r *Repository) UpdatePassword(ctx context.Context, userID int64, hash string) error {
	if _, err := r.db.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		userID, hash); err != nil {
		return fmt.Errorf("settings: update password: %w", err)
	}
	return nil
}
