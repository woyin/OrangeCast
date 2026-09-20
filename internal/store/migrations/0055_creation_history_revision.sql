-- R22：文章创作历史绑定确切 ArticleRevision（C13/U02）。
-- article_revision_id 是专用精确身份（默认空=外部导入/精读历史）。
-- 注意顺序：先回填（确定性 canonical 选择，容忍历史重复行），再建非空唯一索引，
-- 避免旧库同 source_url 多行时 UPDATE 撞唯一索引导致迁移失败。
ALTER TABLE creation_history ADD COLUMN article_revision_id TEXT NOT NULL DEFAULT '';

-- 安全回填：旧数据把修订身份编码在 source_url='revision:<id>'。
-- 仅当 <id> 确实是存在的 ArticleRevision 时回填；同一 source_url 有多行时按
-- 确定性规则选一个 canonical 行（优先 published，再 updated_at/created_at/id），
-- 其余重复旧行保留但 article_revision_id 保持空。外部 source_url（http 等）
-- 与指向不存在修订的行完全不改动。
-- 回填同时校正 legacy 行元数据：画像对齐修订所属 draft 画像、title/content 对齐
-- 确切修订（title 空时回退 draft 标题）。外部/悬空/非 canonical 重复行不动。
UPDATE creation_history
SET article_revision_id = substr(creation_history.source_url, 10),
    editorial_profile_id = COALESCE(
        (SELECT d.editorial_profile_id FROM article_revisions r
         JOIN article_drafts d ON d.id = r.draft_id
         WHERE r.id = substr(creation_history.source_url, 10)),
        creation_history.editorial_profile_id),
    title = COALESCE(
        NULLIF((SELECT r.title FROM article_revisions r
                WHERE r.id = substr(creation_history.source_url, 10)), ''),
        (SELECT d.title FROM article_revisions r
         JOIN article_drafts d ON d.id = r.draft_id
         WHERE r.id = substr(creation_history.source_url, 10)),
        creation_history.title),
    content = COALESCE(
        (SELECT r.markdown FROM article_revisions r
         WHERE r.id = substr(creation_history.source_url, 10)),
        creation_history.content)
WHERE article_revision_id = ''
  AND source_url LIKE 'revision:%'
  AND EXISTS (SELECT 1 FROM article_revisions r WHERE r.id = substr(creation_history.source_url, 10))
  AND id = (
      SELECT h2.id FROM creation_history h2
      WHERE h2.source_url = creation_history.source_url
        AND EXISTS (SELECT 1 FROM article_revisions r2 WHERE r2.id = substr(h2.source_url, 10))
      ORDER BY CASE WHEN h2.status = 'published' THEN 0 ELSE 1 END,
               h2.updated_at DESC, h2.created_at DESC, h2.id
      LIMIT 1
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_creation_history_revision ON creation_history(article_revision_id) WHERE article_revision_id != '';
