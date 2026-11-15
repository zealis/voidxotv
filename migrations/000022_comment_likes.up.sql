-- 评论点赞：一条评论对一个用户只能赞一次，主键即去重约束。
-- 与 reactions 表分开，避免把「内容点赞」和「评论点赞」混进同一组统计。
CREATE TABLE comment_likes (
    comment_id BIGINT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id)
);
CREATE INDEX comment_likes_user_idx ON comment_likes (user_id, comment_id);
