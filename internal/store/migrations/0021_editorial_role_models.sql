-- Migration 0021: editorial roles may override the general analysis model.
-- NULL means inherit analysis_provider / analysis_model, preserving existing installs.

ALTER TABLE settings ADD COLUMN writer_model TEXT;
ALTER TABLE settings ADD COLUMN scout_model TEXT;
ALTER TABLE settings ADD COLUMN evidence_reviewer_model TEXT;
ALTER TABLE settings ADD COLUMN style_editor_model TEXT;
ALTER TABLE settings ADD COLUMN writer_provider TEXT;
ALTER TABLE settings ADD COLUMN scout_provider TEXT;
ALTER TABLE settings ADD COLUMN evidence_reviewer_provider TEXT;
ALTER TABLE settings ADD COLUMN style_editor_provider TEXT;
