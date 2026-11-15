package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/totp"
)

// Aggregator is implemented by the stats module; the API can trigger one
// idempotent aggregation round on demand without waiting for the Worker tick.
type Aggregator interface {
	RunOnce(ctx context.Context) error
}

// StoreResolver signs poster keys for the moderation queue. It may be nil in
// environments without object storage; the UI then shows a neutral placeholder.
type StoreResolver interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// RegisterRoutes mounts all后台 endpoints under /api/v1/admin/.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/auth/login", h.login)
	// 后台二次验证（§11.6 应启用 TOTP）：setup 生成密钥，enable 验证后生效。
	mux.Handle("GET /api/v1/admin/totp", h.wrap(PermSettingsRead, false, h.totpState))
	mux.Handle("POST /api/v1/admin/totp/setup", h.wrap(PermSettingsWrite, true, h.totpSetup))
	mux.Handle("POST /api/v1/admin/totp/enable", h.wrap(PermSettingsWrite, true, h.totpEnable))
	mux.Handle("POST /api/v1/admin/totp/disable", h.wrap(PermSettingsWrite, true, h.totpDisable))
	mux.Handle("POST /api/v1/admin/auth/logout", h.RequireAdmin(http.HandlerFunc(h.logout)))
	mux.Handle("GET /api/v1/admin/auth/me", h.RequireAdmin(http.HandlerFunc(h.me)))
	mux.Handle("GET /api/v1/admin/auth/csrf", h.RequireAdmin(http.HandlerFunc(h.csrf)))

	mux.Handle("GET /api/v1/admin/stats/overview", h.wrap(PermStatsRead, false, h.overview))
	mux.Handle("GET /api/v1/admin/stats/trend", h.wrap(PermStatsRead, false, h.trend))
	mux.Handle("GET /api/v1/admin/stats/hot", h.wrap(PermStatsRead, false, h.hot))
	mux.Handle("POST /api/v1/admin/stats/aggregate", h.wrap(PermStatsAggregate, true, h.aggregate))

	mux.Handle("GET /api/v1/admin/reviews", h.wrap(PermReviewRead, false, h.reviewQueue))
	mux.Handle("GET /api/v1/admin/reviews/{id}", h.wrap(PermReviewRead, false, h.reviewItem))
	mux.Handle("POST /api/v1/admin/reviews/{id}/decision", h.wrap(PermReviewDecide, true, h.decide))
	mux.Handle("POST /api/v1/admin/reviews/batch-approve", h.wrap(PermReviewDecide, true, h.batchApprove))

	// 申诉（§9.4）：与审核共用评审权限，读取需 reviews:read。
	mux.Handle("GET /api/v1/admin/appeals", h.wrap(PermReviewRead, false, h.appeals))
	mux.Handle("POST /api/v1/admin/appeals/{id}/decision", h.wrap(PermReviewDecide, true, h.decideAppeal))

	mux.Handle("GET /api/v1/admin/reports", h.wrap(PermReportRead, false, h.reportCases))
	mux.Handle("POST /api/v1/admin/reports/{id}/resolve", h.wrap(PermReportResolve, true, h.resolveReport))

	mux.Handle("GET /api/v1/admin/users", h.wrap(PermUserRead, false, h.users))
	mux.Handle("POST /api/v1/admin/users/{id}/sanction", h.wrap(PermUserSanction, true, h.sanctionUser))
	mux.Handle("POST /api/v1/admin/users/{id}/avatar", h.wrap(PermUserSanction, true, h.setUserAvatar))

	mux.Handle("GET /api/v1/admin/topics", h.wrap(PermTopicRead, false, h.topics))
	mux.Handle("POST /api/v1/admin/topics", h.wrap(PermTopicWrite, true, h.createTopic))
	mux.Handle("POST /api/v1/admin/topics/{id}/status", h.wrap(PermTopicWrite, true, h.setTopicStatus))
	mux.Handle("POST /api/v1/admin/topics/{id}/merge", h.wrap(PermTopicWrite, true, h.mergeTopics))
	mux.Handle("POST /api/v1/admin/topics/placements", h.wrap(PermTopicWrite, true, h.topicPlacement))

	mux.Handle("GET /api/v1/admin/connectors", h.wrap(PermConnectorRead, false, h.connectors))
	mux.Handle("POST /api/v1/admin/connectors", h.wrap(PermConnectorWrite, true, h.upsertConnector))
	mux.Handle("GET /api/v1/admin/imports", h.wrap(PermConnectorRead, false, h.imports))
	mux.Handle("POST /api/v1/admin/imports", h.wrap(PermConnectorWrite, true, h.createImport))
	mux.Handle("POST /api/v1/admin/connectors/{id}/refresh", h.wrap(PermConnectorWrite, true, h.refreshConnector))

	mux.Handle("GET /api/v1/admin/settings", h.wrap(PermSettingsRead, false, h.settings))
	mux.Handle("GET /api/v1/admin/settings/hot-weights",
		h.wrap(PermSettingsRead, false, h.hotWeights))
	mux.Handle("PUT /api/v1/admin/settings/hot-weights",
		h.wrap(PermSettingsWrite, true, h.updateHotWeights))
	// 功能开关（§4.7）：GIF 表情包的图源与密钥，密钥只存服务端、不下发浏览器。
	mux.Handle("GET /api/v1/admin/settings/gif",
		h.wrap(PermSettingsRead, false, h.gifConfig))
	mux.Handle("PUT /api/v1/admin/settings/gif",
		h.wrap(PermSettingsWrite, true, h.updateGifConfig))
	// 垃圾信息控制：开关 / 自定义词、按需下载唯一一份词库、文本测试。
	mux.Handle("GET /api/v1/admin/settings/spam",
		h.wrap(PermSettingsRead, false, h.spamConfig))
	mux.Handle("PUT /api/v1/admin/settings/spam",
		h.wrap(PermSettingsWrite, true, h.updateSpamConfig))
	mux.Handle("POST /api/v1/admin/settings/spam/lexicon",
		h.wrap(PermSettingsWrite, true, h.downloadSpamLexicon))
	mux.Handle("POST /api/v1/admin/settings/spam/test",
		h.wrap(PermSettingsRead, false, h.testSpam))

	// 账号与权限（§4.7）：只有超管能看/改，改完吊销目标会话强制重新登录。
	mux.Handle("GET /api/v1/admin/accounts", h.wrap(PermAccountRead, false, h.accounts))
	mux.Handle("POST /api/v1/admin/accounts", h.wrap(PermAccountWrite, true, h.createAccount))
	mux.Handle("POST /api/v1/admin/accounts/{id}/role", h.wrap(PermAccountWrite, true, h.setAccountRole))
	mux.Handle("POST /api/v1/admin/accounts/{id}/status", h.wrap(PermAccountWrite, true, h.setAccountStatus))
	mux.Handle("POST /api/v1/admin/accounts/{id}/password", h.wrap(PermAccountWrite, true, h.resetAccountPassword))
	// 自助改密：任何已登录账号都能改自己的口令。
	mux.Handle("POST /api/v1/admin/auth/password", h.wrapSelf(h.changeOwnPassword))

	// 分类配置（§4.7 系统设置）：变更会触发关联页再验证。
	mux.Handle("GET /api/v1/admin/categories", h.wrap(PermSettingsRead, false, h.categories))
	mux.Handle("POST /api/v1/admin/categories", h.wrap(PermSettingsWrite, true, h.upsertCategory))
	mux.Handle("POST /api/v1/admin/categories/{id}/status", h.wrap(PermSettingsWrite, true, h.setCategoryStatus))
	mux.Handle("POST /api/v1/admin/categories/assign", h.wrap(PermSettingsWrite, true, h.assignCategory))
	mux.Handle("GET /api/v1/admin/contents/{id}/categories", h.wrap(PermSettingsRead, false, h.contentCategories))
	mux.Handle("GET /api/v1/admin/audit", h.wrap(PermAuditRead, false, h.auditLog))
	mux.Handle("GET /api/v1/admin/delivery", h.wrap(PermDeliveryAdmin, false, h.delivery))
	mux.Handle("POST /api/v1/admin/delivery/retry", h.wrap(PermDeliveryAdmin, true, h.retryDelivery))
	// Outbox 事件：失败事件的可观测与重放（§10.4）。
	mux.Handle("GET /api/v1/admin/outbox", h.wrap(PermDeliveryAdmin, false, h.outboxEvents))
	mux.Handle("POST /api/v1/admin/outbox/retry", h.wrap(PermDeliveryAdmin, true, h.retryOutbox))
}

// ── 后台会话 ──────────────────────────────────────────────────────

type loginRequest struct {
	// 登录只认邮箱，与前台账号同一口径（0.17.0 起后台不再有用户名）。
	Email    string `json:"email"`
	Password string `json:"password"`
	// TOTPCode 是启用二次验证后的 6 位动态码（§11.6）。
	TOTPCode string `json:"totp_code"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	ip := clientIP(r)
	allowed, err := h.sessions.LoginAllowed(r.Context(), ip)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !allowed {
		httpserver.Error(w, http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请稍后再试",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	account, err := h.repo.FindActiveAdmin(r.Context(), auth.NormalizeEmail(request.Email))
	if err != nil {
		h.sessions.NoteLoginFailure(r.Context(), ip)
		h.writeError(w, r, ErrBadLogin)
		return
	}
	ok, err := verifyLoginPassword(account.PasswordHash, request.Password)
	if err != nil || !ok {
		h.sessions.NoteLoginFailure(r.Context(), ip)
		h.writeError(w, r, ErrBadLogin)
		return
	}
	// 二次验证（§11.6）：启用 TOTP 的账号必须带有效动态码；
	// 提示 totp_required 让前端弹出输入框，同时不泄露「密码已正确」以外的信息。
	if account.TOTPSecret != "" {
		if !totp.Validate(account.TOTPSecret, strings.TrimSpace(request.TOTPCode), time.Now()) {
			h.sessions.NoteLoginFailure(r.Context(), ip)
			httpserver.Error(w, http.StatusUnauthorized, "totp_required",
				"请输入动态验证码", httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}
	csrf, err := newToken()
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	now := time.Now().UTC()
	token, err := h.sessions.Create(r.Context(), sessionRecord{
		AdminID: account.ID, Email: account.Email, Role: account.Role,
		CSRF: csrf, CreatedAt: now,
		AbsoluteExpiresAt: now.Add(h.cfg.SessionAbsoluteTTL),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.sessions.ClearLoginFailures(r.Context(), ip)
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieName(), Value: token, Path: "/",
		HttpOnly: true, Secure: h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  now.Add(h.cfg.SessionAbsoluteTTL),
	})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": account.ID, "email": account.Email, "role": account.Role,
		"permissions": permissionsFor(account.Role), "csrf_token": csrf,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(h.cookieName())
	if err == nil {
		_ = h.sessions.Destroy(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(), Value: "", Path: "/",
		HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(0, 0)})
	httpserver.JSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": principal.AdminID, "email": principal.Email, "role": principal.Role,
		"permissions": permissionsFor(principal.Role), "csrf_token": principal.CSRF,
	})
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	httpserver.JSON(w, http.StatusOK, map[string]any{"csrf_token": principal.CSRF})
}

// ── 数据概览 ──────────────────────────────────────────────────────

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	result, err := h.repo.Overview(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, result)
}

func (h *Handler) trend(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 30 {
		days = 7
	}
	points, err := h.repo.Trend(r.Context(), days)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"days": days, "items": points})
}

func (h *Handler) hot(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 10, 1, 50)
	items, err := h.repo.Hot(r.Context(), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (h *Handler) aggregate(w http.ResponseWriter, r *http.Request) {
	if h.aggregator == nil {
		h.writeError(w, r, ErrConflict)
		return
	}
	if err := h.aggregator.RunOnce(r.Context()); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "stats.aggregate", "stats", "overview", nil, map[string]any{"trigger": "manual"})
	httpserver.JSON(w, http.StatusOK, map[string]any{"aggregated": true, "at": time.Now().UTC()})
}

// ── 内容审核 ──────────────────────────────────────────────────────

func (h *Handler) reviewQueue(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 20, 1, 100)
	items, err := h.repo.ReviewQueue(r.Context(), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.resolvePosters(r, items)
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (h *Handler) reviewItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.repo.ReviewItem(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	items := []ReviewItem{item}
	h.resolvePosters(r, items)
	httpserver.JSON(w, http.StatusOK, items[0])
}

func (h *Handler) resolvePosters(r *http.Request, items []ReviewItem) {
	if h.store == nil {
		return
	}
	for i := range items {
		if items[i].PosterKey == "" {
			continue
		}
		url, err := h.store.PresignGet(r.Context(), items[i].PosterKey, 15*time.Minute)
		if err == nil {
			items[i].PosterURL = url
		}
	}
}

type decisionRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request decisionRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	result, err := h.repo.ApplyDecision(r.Context(), id, principal.AdminID,
		request.Action, request.Reason)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "review."+request.Action, "content", id,
		map[string]any{"review_status": "before"}, result)
	httpserver.JSON(w, http.StatusOK, result)
}

func (h *Handler) batchApprove(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs []int64 `json:"ids"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > 50 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	principal, _ := principalFrom(r.Context())
	approved, err := h.repo.BatchApprove(r.Context(), request.IDs, principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 批量操作只允许通过（UI 规范 §11.5）。
	h.audit(r, "review.batch_approve", "content", "batch",
		map[string]any{"requested": len(request.IDs)}, map[string]any{"approved": approved})
	httpserver.JSON(w, http.StatusOK, map[string]any{"requested": len(request.IDs), "approved": approved})
}

// ── 举报中心 ──────────────────────────────────────────────────────

func (h *Handler) reportCases(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 20, 1, 100)
	items, err := h.repo.ListReportCases(r.Context(), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

type resolveReportRequest struct {
	Outcome      string `json:"outcome"`
	LinkedAction string `json:"linked_action"`
	Resolution   string `json:"resolution"`
}

func (h *Handler) resolveReport(w http.ResponseWriter, r *http.Request) {
	caseID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request resolveReportRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	result, err := h.repo.ResolveReportCase(r.Context(), caseID, principal.AdminID,
		request.Outcome, request.LinkedAction, request.Resolution)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "report.resolve", "report_case", caseID,
		map[string]any{"status": "pending"}, result)
	// 用户处置联动（举报对象为用户时）单独留痕。
	if request.Outcome == "upheld" && result.ObjectType == "user" &&
		(request.LinkedAction == "warn" || request.LinkedAction == "ban" ||
			request.LinkedAction == "mute") {
		if _, err := h.repo.SanctionUser(r.Context(), result.ObjectID, principal.AdminID,
			request.LinkedAction, request.Resolution); err == nil {
			h.audit(r, "user."+request.LinkedAction, "user", result.ObjectID,
				nil, map[string]any{"source": "report", "case_id": caseID})
			if request.LinkedAction == "ban" {
				_ = h.sessions.RevokeUserSessions(r.Context(), result.ObjectID)
			}
		}
	}
	// 评论举报成立且联动移除评论时单独留痕，便于追查是谁移除了哪条评论。
	if request.Outcome == "upheld" && result.ObjectType == "comment" &&
		request.LinkedAction == "remove_comment" {
		h.audit(r, "comment.remove", "comment", result.ObjectID,
			nil, map[string]any{"source": "report", "case_id": caseID})
	}
	httpserver.JSON(w, http.StatusOK, result)
}

// ── 用户管理 ──────────────────────────────────────────────────────

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 20, 1, 100)
	items, err := h.repo.ListUsers(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

type sanctionRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func (h *Handler) sanctionUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request sanctionRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	result, err := h.repo.SanctionUser(r.Context(), userID, principal.AdminID,
		request.Action, request.Reason)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 封禁立即使会话失效（§4.7）。
	if request.Action == "ban" {
		_ = h.sessions.RevokeUserSessions(r.Context(), userID)
	}
	h.audit(r, "user."+request.Action, "user", userID, nil, result)
	httpserver.JSON(w, http.StatusOK, result)
}

// ── 话题运营 ──────────────────────────────────────────────────────

func (h *Handler) topics(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListTopics(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	item, err := h.repo.CreateTopic(r.Context(), request.Slug, request.Name)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "topic.create", "topic", item.ID, nil, item)
	httpserver.JSON(w, http.StatusCreated, item)
}

func (h *Handler) setTopicStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	item, err := h.repo.SetTopicStatus(r.Context(), id, request.Status)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "topic.status", "topic", id, nil, item)
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) mergeTopics(w http.ResponseWriter, r *http.Request) {
	sourceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request struct {
		TargetID int64 `json:"target_id"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	moved, err := h.repo.MergeTopics(r.Context(), sourceID, request.TargetID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "topic.merge", "topic", sourceID,
		map[string]any{"target_id": request.TargetID}, map[string]any{"moved": moved})
	httpserver.JSON(w, http.StatusOK, map[string]any{"moved": moved, "source_id": sourceID,
		"target_id": request.TargetID})
}

func (h *Handler) topicPlacement(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TopicID     int64  `json:"topic_id"`
		Placement   string `json:"placement"`
		EffectiveAt string `json:"effective_at"`
		// Enabled 缺省为 true，保持「设置运营位」的旧调用不变；
		// 显式传 false 表示取消该运营位。
		Enabled *bool `json:"enabled"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	effective := time.Now().UTC()
	if request.EffectiveAt != "" {
		parsed, err := time.Parse(time.RFC3339, request.EffectiveAt)
		if err != nil {
			h.writeError(w, r, ErrInvalidInput)
			return
		}
		effective = parsed
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	principal, _ := principalFrom(r.Context())
	if err := h.repo.SetTopicPlacement(r.Context(), request.TopicID, principal.AdminID,
		request.Placement, effective, enabled); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "topic.placement", "topic", request.TopicID, nil,
		map[string]any{"placement": request.Placement, "enabled": enabled,
			"effective_at": effective})
	httpserver.JSON(w, http.StatusOK, map[string]any{"placement": request.Placement,
		"enabled": enabled, "effective_at": effective})
}

// ── 混合来源与系统设置 ────────────────────────────────────────────

func (h *Handler) connectors(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListConnectors(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (h *Handler) upsertConnector(w http.ResponseWriter, r *http.Request) {
	var request ConnectorInput
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := validateConnectorURL(request.BaseURL); err != nil {
		h.writeError(w, r, err)
		return
	}
	item, err := h.repo.UpsertConnector(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "connector.upsert", "connector", item.ID, nil,
		map[string]any{"slug": item.Slug, "kind": item.Kind, "status": item.Status})
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) refreshConnector(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.repo.RefreshConnector(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "connector.refresh", "connector", id, nil, map[string]any{"at": time.Now().UTC()})
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) imports(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 20, 1, 100)
	items, err := h.repo.ListImports(r.Context(), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (h *Handler) createImport(w http.ResponseWriter, r *http.Request) {
	var request ImportInput
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	// §4.6：普通用户不能提交任意内网 URL；后台自定义地址也必须过 SSRF 校验。
	if err := validateConnectorURL(request.SourceURL); err != nil {
		h.writeError(w, r, err)
		return
	}
	if request.IdempotencyKey == "" || request.ConnectorID <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.repo.CreateImport(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "import.create", "import_job", item.ID, nil,
		map[string]any{"connector_id": item.ConnectorID, "external_id": item.ExternalID})
	httpserver.JSON(w, http.StatusCreated, item)
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	diagnostics, err := h.repo.Diagnostics(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 密钥只在服务端读取，前端只显示脱敏状态（§4.7）。
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"storage": map[string]any{
			"endpoint": h.cfg.S3Endpoint, "bucket": h.cfg.S3Bucket,
			"public_read":     h.cfg.S3PublicRead,
			"keys_configured": h.cfg.S3AccessKey != "" && h.cfg.S3SecretKey != "",
		},
		"delivery": map[string]any{
			"nuxt_origin":          h.cfg.DeliveryNuxtOrigin,
			"token_configured":     h.cfg.DeliveryToken != "",
			"debounce":             h.cfg.DeliveryDebounce.String(),
			"home_debounce":        h.cfg.DeliveryHomeDebounce.String(),
			"cdn_purge_configured": h.cfg.CDNZoneID != "" && h.cfg.CDNAPIToken != "",
		},
		"transcoding": map[string]any{
			"short_video_max_ms": h.cfg.ShortVideoMaxMS,
			"max_upload_mb":      h.cfg.MaxUploadBytes / (1024 * 1024),
			"ffmpeg_configured":  h.cfg.FFmpegPath != "",
			"profile_version":    "h264-aac-v1",
		},
		"site":         map[string]any{"site_url": h.cfg.SiteURL, "environment": h.cfg.Env},
		"hot_weights":  h.hotWeightsMap(r.Context()),
		"risk_weights": map[string]int{"report": 10, "machine": 5, "copyright": 20},
		"diagnostics":  diagnostics,
	})
}

// hotWeightsMap 读热门榜权重；读取失败回落默认值并记日志（§7.6）。
func (h *Handler) hotWeightsMap(ctx context.Context) map[string]float64 {
	play, like, err := h.repo.HotWeights(ctx)
	if err != nil {
		h.log.Warn("hot weights read failed", "error", err)
		play, like = 1, 3
	}
	return map[string]float64{"play_weight": play, "like_weight": like}
}

// hotWeights 独立端点：设置页的权重表单读取。
func (h *Handler) hotWeights(w http.ResponseWriter, r *http.Request) {
	httpserver.JSON(w, http.StatusOK, h.hotWeightsMap(r.Context()))
}

// updateHotWeights 校验并保存权重；写审计（§11.6 敏感配置变更）。
func (h *Handler) updateHotWeights(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PlayWeight *float64 `json:"play_weight"`
		LikeWeight *float64 `json:"like_weight"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", err.Error(),
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	play, like := request.PlayWeight, request.LikeWeight
	if play == nil || like == nil || *play < 0 || *play > 1000 || *like < 0 || *like > 1000 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", "权重需为 0–1000 的数值",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	principal, _ := principalFrom(r.Context())
	before := h.hotWeightsMap(r.Context())
	if err := h.repo.SetHotWeights(r.Context(), principal.AdminID, *play, *like); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "settings.hot_weights", "system_setting", "hot", before,
		map[string]float64{"play_weight": *play, "like_weight": *like})
	httpserver.JSON(w, http.StatusOK, map[string]float64{"play_weight": *play, "like_weight": *like})
}

// ── 后台 TOTP（§11.6）────────────────────────────────────────────

// totpState 回显当前账号是否已启用二次验证。
func (h *Handler) totpState(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	secret, err := h.repo.TOTPSecret(r.Context(), principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"enabled": secret != ""})
}

// totpSetup 生成新密钥并直接返回；密钥只有在 enable 验证通过后才落库，
// 避免「存了密钥但 App 没扫上」把自己锁在门外。
func (h *Handler) totpSetup(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	secret, err := totp.GenerateSecret()
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_url": totp.OTPAuthURI(secret, "yejie-admin", principal.Email),
	})
}

// totpEnable 用客户端提交的密钥 + 当前动态码完成绑定。
func (h *Handler) totpEnable(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	if !totp.Validate(request.Secret, strings.TrimSpace(request.Code), time.Now()) {
		httpserver.Error(w, http.StatusBadRequest, "invalid_code", "动态码不正确，请重试",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	principal, _ := principalFrom(r.Context())
	if err := h.repo.SetTOTPSecret(r.Context(), principal.AdminID, request.Secret); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "totp.enable", "admin_user", principal.AdminID, nil, nil)
	httpserver.JSON(w, http.StatusOK, map[string]bool{"enabled": true})
}

// totpDisable 需要当前动态码才能关闭，防止拿到会话的人静默关闭二次验证。
func (h *Handler) totpDisable(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code string `json:"code"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	secret, err := h.repo.TOTPSecret(r.Context(), principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if secret == "" {
		httpserver.Error(w, http.StatusBadRequest, "not_enabled", "尚未启用二次验证",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if !totp.Validate(secret, strings.TrimSpace(request.Code), time.Now()) {
		httpserver.Error(w, http.StatusBadRequest, "invalid_code", "动态码不正确，请重试",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if err := h.repo.SetTOTPSecret(r.Context(), principal.AdminID, ""); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "totp.disable", "admin_user", principal.AdminID, nil, nil)
	httpserver.JSON(w, http.StatusOK, map[string]bool{"enabled": false})
}

func (h *Handler) auditLog(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 30, 1, 100)
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	actionPrefix := strings.TrimSpace(r.URL.Query().Get("action"))
	if len(actionPrefix) > 40 {
		actionPrefix = actionPrefix[:40]
	}
	items, err := h.repo.ListAudit(r.Context(), int64(limit), cursor, actionPrefix)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var next any
	if len(items) == limit {
		next = items[len(items)-1].ID
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"count": len(items), "items": items, "next_cursor": next,
	})
}

func (h *Handler) delivery(w http.ResponseWriter, r *http.Request) {
	diagnostics, err := h.repo.Diagnostics(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, diagnostics)
}

func (h *Handler) retryDelivery(w http.ResponseWriter, r *http.Request) {
	count, err := h.repo.RetryDeadLetters(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "delivery.retry", "delivery_job", "dead_letter", nil,
		map[string]any{"retried": count})
	httpserver.JSON(w, http.StatusOK, map[string]any{"retried": count})
}

// outboxEvents 列出 Outbox 事件，供后台排查「内容变了但页面没失效」。
func (h *Handler) outboxEvents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "failed", "pending", "processing", "done":
	default:
		h.writeError(w, r, fmt.Errorf("%w: status 只能是 failed / pending / processing / done", ErrInvalidInput))
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 20, 1, 100)
	items, err := h.repo.ListOutboxEvents(r.Context(), status, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

// retryOutbox 把失败事件重置回 pending：ids 为空表示重放全部失败事件。
func (h *Handler) retryOutbox(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs []int64 `json:"ids"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(request.IDs) > 20 {
		h.writeError(w, r, fmt.Errorf("%w: 单次最多重放 20 条事件", ErrInvalidInput))
		return
	}
	count, err := h.repo.RetryOutboxEvents(r.Context(), request.IDs)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	target := "failed"
	if len(request.IDs) > 0 {
		target = fmt.Sprintf("ids:%d", len(request.IDs))
	}
	h.audit(r, "outbox.retry", "outbox_event", target, nil, map[string]any{"retried": count})
	httpserver.JSON(w, http.StatusOK, map[string]any{"retried": count})
}

func parseInt(raw string, fallback, min, max int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return fallback
	}
	return value
}
