// Package settings owns the user-facing preferences page (§11.2：账号 / 隐私 /
// 通知 / 播放与材质 / 通用). It also exposes the profile fields the same page edits.
package settings

import (
	"errors"
	"time"
)

// ErrInvalidInput 由校验层返回，handler 统一映射为 400 invalid_input。
var ErrInvalidInput = errors.New("设置项不合法")

// Settings 是「设置页」的完整状态；缺行时按 Default() 返回，前端无需区分「未设置」。
type Settings struct {
	NotifyInteraction bool `json:"notify_interaction"`
	NotifyFollow      bool `json:"notify_follow"`
	NotifySystem      bool `json:"notify_system"`
	// QuietStart / QuietEnd 是当天分钟偏移（0–1439），两者相等表示免打扰关闭。
	QuietStart        int    `json:"quiet_start"`
	QuietEnd          int    `json:"quiet_end"`
	DefaultVisibility string `json:"default_visibility"`
	Autoplay          bool   `json:"autoplay"`
	ReduceMotion      bool   `json:"reduce_motion"`
	// Theme 只影响工具型页面（§14）：system = 跟随系统，light / dark 为用户显式选择。
	// 沉浸界面恒为深色，不读此字段。
	Theme     string    `json:"theme"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Patch 是部分更新：nil 表示「本次不改这一项」。
type Patch struct {
	NotifyInteraction *bool   `json:"notify_interaction"`
	NotifyFollow      *bool   `json:"notify_follow"`
	NotifySystem      *bool   `json:"notify_system"`
	QuietStart        *int    `json:"quiet_start"`
	QuietEnd          *int    `json:"quiet_end"`
	DefaultVisibility *string `json:"default_visibility"`
	Autoplay          *bool   `json:"autoplay"`
	ReduceMotion      *bool   `json:"reduce_motion"`
	Theme             *string `json:"theme"`
}

// Default 必须与迁移里的列默认值保持一致，否则「没设置过」与「重置」会出现两种结果。
func Default() Settings {
	return Settings{
		NotifyInteraction: true,
		NotifyFollow:      true,
		NotifySystem:      true,
		QuietStart:        0,
		QuietEnd:          0,
		DefaultVisibility: "public",
		Autoplay:          true,
		ReduceMotion:      false,
		Theme:             "system",
	}
}

// ValidVisibility 与 contents.visibility 的取值域对齐。
func ValidVisibility(value string) bool {
	return value == "public" || value == "friends" || value == "private"
}

// ValidTheme 与 user_settings.theme 的 CHECK 约束保持一致（§14）。
func ValidTheme(value string) bool {
	return value == "system" || value == "light" || value == "dark"
}

// Profile 是设置页「账号」分组展示与编辑的字段。邮箱只读，仅本人可见。
type Profile struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	Email       string `json:"email"`
	// AvatarKey 只在服务端流转：对外一律给可访问地址，不泄漏对象键。
	AvatarKey string `json:"-"`
	AvatarURL string `json:"avatar_url"`
	// VerifiedCreator 给自己页面用：已认证创作者在昵称旁挂标签。
	VerifiedCreator bool      `json:"verified_creator"`
	CreatedAt       time.Time `json:"created_at"`
}
