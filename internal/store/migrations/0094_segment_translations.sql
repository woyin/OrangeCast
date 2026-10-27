-- 0094 对齐译文（Translation）：把冻结转录版本按 Segment 对齐翻译，不覆盖原语言。
-- 译文是 CitedDerivative：每行都必须指向原版本真实存在的 Segment；缺译的 Segment
-- 保留原文并显式缺失，不由程序补译。身份 = 来源 + 转录版本 + 目标语言。
CREATE TABLE IF NOT EXISTS segment_translations (
    source_type       TEXT NOT NULL,
    source_id         TEXT NOT NULL,
    transcript_version INTEGER NOT NULL,
    target_language   TEXT NOT NULL,
    segment_id        TEXT NOT NULL,
    text              TEXT NOT NULL,
    provider          TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    job_id            TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (source_type, source_id, transcript_version, target_language, segment_id)
);
CREATE INDEX IF NOT EXISTS idx_segment_translations_source ON segment_translations(source_type, source_id, target_language);
CREATE TRIGGER segment_translations_episode_purge AFTER DELETE ON episodes BEGIN
  DELETE FROM segment_translations WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER segment_translations_upload_purge AFTER DELETE ON uploads BEGIN
  DELETE FROM segment_translations WHERE source_type='upload' AND source_id=old.id;
END;
