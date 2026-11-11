package content

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// Service enforces the publishing and visibility rules from 开发技术规范 §3.2 / §6.1.
type Service struct {
	repo *Repository
	now  func() time.Time
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

func (s *Service) CreateDraft(ctx context.Context, authorID int64, input CreateDraftInput) (Content, error) {
	if err := validateTypeAndText(input.Type, input.Title, input.Description); err != nil {
		return Content{}, err
	}
	return s.repo.Create(ctx, authorID, input)
}

func (s *Service) UpdateDraft(ctx context.Context, contentID, authorID int64, input UpdateDraftInput) (Content, error) {
	existing, err := s.Owned(ctx, contentID, authorID)
	if err != nil {
		return Content{}, err
	}
	if existing.PublishState != "draft" {
		return Content{}, ErrNotDraft
	}
	if utf8.RuneCountInString(input.Title) > maxTitleRunes ||
		utf8.RuneCountInString(input.Description) > maxDescriptionRunes {
		return Content{}, errors.Join(ErrInvalidInput, ErrTitleTooLong)
	}
	if input.Visibility == "" {
		input.Visibility = "public"
	}
	if !validVisibility(input.Visibility) {
		return Content{}, ErrVisibility
	}
	return s.repo.UpdateDraft(ctx, contentID, authorID, input)
}

func (s *Service) Publish(ctx context.Context, contentID, authorID int64, input PublishInput) (Content, error) {
	if input.Visibility == "" {
		input.Visibility = "public"
	}
	if !validVisibility(input.Visibility) {
		return Content{}, ErrVisibility
	}
	if input.ScheduledAt != nil {
		lead := input.ScheduledAt.Sub(s.now().UTC())
		if lead < minScheduleLead {
			return Content{}, ErrScheduleSoon
		}
	}
	// §3.2 沉浸流准入：图集必须是 2–18 张 ready 图片，定时发布同样适用。
	kind, count, err := s.repo.GalleryStats(ctx, contentID, authorID)
	if err != nil {
		return Content{}, err
	}
	if kind == "image" && (count < minGalleryImages || count > maxGalleryImages) {
		return Content{}, ErrGallerySize
	}
	return s.repo.SetPublish(ctx, contentID, authorID, input)
}

// Visible implements 404 semantics for private/friends/not-yet-approved content.
func (s *Service) Visible(ctx context.Context, content Content, viewerID int64) (bool, error) {
	if content.PublishState != "published" || content.ReviewStatus != "approved" {
		return false, nil
	}
	if viewerID == content.AuthorID {
		return true, nil
	}
	switch content.Visibility {
	case "public":
		return true, nil
	case "private":
		return false, nil
	case "friends":
		if viewerID == 0 {
			return false, nil
		}
		return s.repo.AreMutualFollows(ctx, viewerID, content.AuthorID)
	default:
		return false, nil
	}
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (Content, error) {
	return s.repo.GetBySlug(ctx, slug)
}

func (s *Service) Owned(ctx context.Context, contentID, viewerID int64) (Content, error) {
	content, err := s.repo.GetByID(ctx, contentID)
	if err != nil {
		return Content{}, err
	}
	if content.AuthorID != viewerID || content.PublishState == "deleted" {
		return Content{}, ErrNotFound // do not reveal existence
	}
	return content, nil
}

// TranscodeStatuses 批量取视频内容的最新转码状态（§10 处理中徽标）。
func (s *Service) TranscodeStatuses(ctx context.Context, contentIDs []int64) (map[int64]string, error) {
	return s.repo.TranscodeStatuses(ctx, contentIDs)
}

func (s *Service) ListMine(ctx context.Context, authorID int64, limit, offset int,
	state, order string) ([]Content, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListByAuthor(ctx, authorID, limit, offset, state, order)
}

func validateTypeAndText(kind, title, description string) error {
	if kind != "video" && kind != "image" {
		return ErrInvalidType
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		return ErrTitleTooLong
	}
	if utf8.RuneCountInString(description) > maxDescriptionRunes {
		return ErrBodyTooLong
	}
	return nil
}

func validVisibility(value string) bool {
	return value == "public" || value == "friends" || value == "private"
}

// POIObject is the shape accepted from the publish page (开发技术规范 §6.1).
type POIObject struct {
	Name      string   `json:"name"`
	Address   string   `json:"address"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

func NormalizePOI(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	var poi POIObject
	if err := json.Unmarshal(raw, &poi); err != nil {
		return nil, errors.Join(ErrInvalidInput, err)
	}
	return json.Marshal(poi)
}

// ── 「我」页面（UI 规范 §10）─────────────────────────────────────

// ReactedItem 在作品之外补上作者 handle，供「喜欢 / 收藏」列表直接跳转。
type ReactedItem struct {
	Content
	AuthorHandle string `json:"author_handle"`
}

func (s *Service) UserCounts(ctx context.Context, userID int64) (UserCounts, error) {
	return s.repo.UserCounts(ctx, userID)
}

func (s *Service) ReactedContents(ctx context.Context, userID int64, kind string, limit int) ([]ReactedItem, error) {
	if kind != "like" && kind != "favorite" {
		return nil, ErrInvalidType
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items, err := s.repo.ReactedContents(ctx, userID, kind, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.AuthorID)
	}
	handles, err := s.repo.AuthorHandles(ctx, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]ReactedItem, 0, len(items))
	for _, item := range items {
		rows = append(rows, ReactedItem{Content: item, AuthorHandle: handles[item.AuthorID]})
	}
	return rows, nil
}
