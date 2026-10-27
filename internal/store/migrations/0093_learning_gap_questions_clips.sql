-- 0093 学习补齐：ReviewQuestion（自我检验问题）、HighlightClip（高光真实音频区间）、
-- podcast.opml_present（订阅是否随 OPML 导入，仅作展示来源，不改变处理策略）。
--
-- 三张/项都只承载"来源的现场产物"与"程序计算的区间"：
--   review_questions.citations_json 必须指向当前转录版本中真实存在的 Segment；
--   highlight_clips 的起止由源音频 + Segment 时间计算，AI 不得自估；
--   原始音频按 sha256 命名，同一音频只缓存一份，普通 GET 不触发 ffmpeg。

CREATE TABLE IF NOT EXISTS review_questions (
    id              TEXT PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    source_title    TEXT NOT NULL DEFAULT '',
    transcript_version INTEGER NOT NULL,
    prompt          TEXT NOT NULL,
    answer_hint     TEXT NOT NULL DEFAULT '',
    citations_json  TEXT NOT NULL,
    relation_kind   TEXT NOT NULL CHECK(relation_kind IN('citation','reference')),
    time_start      REAL NOT NULL DEFAULT 0,
    time_end        REAL NOT NULL DEFAULT 0,
    provider        TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    job_id          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_review_questions_source ON review_questions(source_type, source_id, created_at);
CREATE TRIGGER review_questions_episode_purge AFTER DELETE ON episodes BEGIN
  DELETE FROM review_questions WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER review_questions_upload_purge AFTER DELETE ON uploads BEGIN
  DELETE FROM review_questions WHERE source_type='upload' AND source_id=old.id;
END;

CREATE TABLE IF NOT EXISTS highlight_clips (
    id              TEXT PRIMARY KEY,
    audio_sha256    TEXT NOT NULL,
    start_seconds   REAL NOT NULL,
    end_seconds     REAL NOT NULL,
    format          TEXT NOT NULL DEFAULT 'mp3',
    rel_path        TEXT NOT NULL,
    bytes           INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'ready',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(audio_sha256, start_seconds, end_seconds, format)
);
CREATE INDEX IF NOT EXISTS idx_highlight_clips_audio ON highlight_clips(audio_sha256, start_seconds);

ALTER TABLE podcasts ADD COLUMN opml_present INTEGER NOT NULL DEFAULT 0;
