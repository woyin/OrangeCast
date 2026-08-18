-- ADR-0022: additive storage for the learning/creation workspace migration.
-- Existing Article* and Evidence* records remain authoritative compatibility data.

ALTER TABLE keypoint_index ADD COLUMN quality_status TEXT NOT NULL DEFAULT 'needs_review';
ALTER TABLE keypoint_index ADD COLUMN stale_at TEXT;
ALTER TABLE keypoint_index ADD COLUMN stale_reason TEXT;

CREATE TABLE material_candidates (
    id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    origin_kind TEXT NOT NULL,
    origin_id TEXT,
    content TEXT NOT NULL,
    citations_json TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending',
    rejection_reason TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    reviewed_at TEXT
);
CREATE INDEX idx_material_candidates_source ON material_candidates(source_type, source_id, status, created_at);

CREATE TABLE material_changes (
    id TEXT PRIMARY KEY,
    keypoint_id TEXT NOT NULL REFERENCES keypoint_index(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    change_kind TEXT NOT NULL,
    snapshot_hash TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(keypoint_id, change_kind, snapshot_hash)
);
CREATE INDEX idx_material_changes_source ON material_changes(source_type, source_id, created_at);

CREATE TABLE owner_notes (
    id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    citations_json TEXT NOT NULL DEFAULT '[]',
    references_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_owner_notes_source ON owner_notes(source_type, source_id, kind, created_at);

CREATE TABLE editorial_relevance (
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE CASCADE,
    keypoint_id TEXT NOT NULL REFERENCES keypoint_index(id) ON DELETE CASCADE,
    assessment TEXT NOT NULL,
    owner_override TEXT,
    rationale TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(editorial_profile_id, keypoint_id)
);

CREATE TABLE rights_constraints (
    id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    constraint_kind TEXT NOT NULL,
    details TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, constraint_kind)
);

CREATE TABLE discovery_settings (
    editorial_profile_id TEXT PRIMARY KEY REFERENCES editorial_profiles(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    daily_limit INTEGER NOT NULL DEFAULT 1,
    batch_budget_cents INTEGER,
    debounce_minutes INTEGER NOT NULL DEFAULT 30,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE proposal_batches (
    id TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'ready',
    window_start_at TEXT,
    material_snapshot_json TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    shortage_reason TEXT NOT NULL DEFAULT '',
    provider TEXT,
    model TEXT,
    cost_cents INTEGER,
    failure_reason TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT
);
CREATE INDEX idx_proposal_batches_profile_status ON proposal_batches(editorial_profile_id, status, created_at);

CREATE TABLE creation_proposals (
    id TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
    proposal_batch_id TEXT REFERENCES proposal_batches(id) ON DELETE SET NULL,
    ideation_session_id TEXT,
    status TEXT NOT NULL DEFAULT 'proposed',
    creation_form TEXT NOT NULL DEFAULT 'article',
    working_title TEXT NOT NULL,
    proposed_claim TEXT NOT NULL,
    owner_claim TEXT,
    audience TEXT NOT NULL DEFAULT '',
    rationale TEXT NOT NULL DEFAULT '',
    material_ids_json TEXT NOT NULL DEFAULT '[]',
    history_relationship TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_creation_proposals_profile_status ON creation_proposals(editorial_profile_id, status, created_at);

CREATE TABLE creation_history (
    id TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    creation_form TEXT NOT NULL DEFAULT 'article',
    title TEXT NOT NULL,
    core_claim TEXT NOT NULL DEFAULT '',
    audience TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE ideation_sessions (
    id TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
    intent TEXT NOT NULL,
    constraints_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE material_diagnoses (
    id TEXT PRIMARY KEY,
    ideation_session_id TEXT NOT NULL REFERENCES ideation_sessions(id) ON DELETE CASCADE,
    diagnosis_json TEXT NOT NULL,
    material_snapshot_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE research_needs (
    id TEXT PRIMARY KEY,
    creation_proposal_id TEXT NOT NULL REFERENCES creation_proposals(id) ON DELETE CASCADE,
    severity TEXT NOT NULL,
    question TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    resolution_source_id TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at TEXT
);
CREATE TABLE research_plans (
    id TEXT PRIMARY KEY,
    research_need_id TEXT NOT NULL REFERENCES research_needs(id) ON DELETE CASCADE,
    question TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    budget_cents INTEGER,
    status TEXT NOT NULL DEFAULT 'draft',
    owner_confirmed_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE creation_briefs (
    id TEXT PRIMARY KEY,
    creation_proposal_id TEXT NOT NULL REFERENCES creation_proposals(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'draft',
    owner_claim TEXT NOT NULL,
    claim_plan_json TEXT NOT NULL DEFAULT '[]',
    material_plan_json TEXT NOT NULL DEFAULT '[]',
    research_need_ids_json TEXT NOT NULL DEFAULT '[]',
    outline TEXT NOT NULL DEFAULT '',
    style TEXT NOT NULL DEFAULT '',
    target_length INTEGER,
    confirmed_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE claim_maps (
    id TEXT PRIMARY KEY,
    work_revision_id TEXT NOT NULL,
    claim_kind TEXT NOT NULL,
    excerpt TEXT NOT NULL,
    keypoint_ids_json TEXT NOT NULL DEFAULT '[]',
    owner_claim TEXT NOT NULL DEFAULT '',
    verified_fact_source_ids_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_claim_maps_revision ON claim_maps(work_revision_id);

CREATE TABLE claim_reviews (
    id TEXT PRIMARY KEY,
    work_revision_id TEXT NOT NULL,
    status TEXT NOT NULL,
    issues_json TEXT NOT NULL DEFAULT '[]',
    provider TEXT,
    model TEXT,
    prompt_version TEXT,
    cost_cents INTEGER,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_claim_reviews_revision ON claim_reviews(work_revision_id, created_at);
