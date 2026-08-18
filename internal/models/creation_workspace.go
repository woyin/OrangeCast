package models

// KeyPointQualityStatus controls whether a learning result is eligible for discovery.
type KeyPointQualityStatus string

// KeyPoint quality states control whether a learning result is eligible for discovery.
const (
	KeyPointNeedsReview      KeyPointQualityStatus = "needs_review"
	KeyPointReady            KeyPointQualityStatus = "ready"
	KeyPointOwnerConfirmed   KeyPointQualityStatus = "owner_confirmed"
	KeyPointQualityDismissed KeyPointQualityStatus = "dismissed"
)

// MaterialCandidate is an unapproved learning insight awaiting the Source-level quality gate.
type MaterialCandidate struct {
	ID, SourceType, SourceID, OriginKind, OriginID, Content, CitationsJSON, Status string
	RejectionReason                                                                *string
	CreatedAt, ReviewedAt                                                          string
}

// MaterialChange records a substantive, idempotent discovery-input change.
type MaterialChange struct{ ID, KeyPointID, SourceType, SourceID, ChangeKind, SnapshotHash, CreatedAt string }

// OwnerNote separates faithful source notes from personal reflections.
type OwnerNote struct{ ID, SourceType, SourceID, Kind, Content, CitationsJSON, ReferencesJSON, CreatedAt, UpdatedAt string }

// EditorialRelevance relates reusable material to a profile without granting source permission.
type EditorialRelevance struct{ EditorialProfileID, KeyPointID, Assessment, OwnerOverride, Rationale, UpdatedAt string }

// RightsConstraint limits external reuse of specific expressions or assets without revoking internal learning use.
type RightsConstraint struct {
	ID, SourceType, SourceID, ConstraintKind, Details, CreatedAt string
	Active                                                       bool
}

// DiscoverySettings is the profile-scoped, explicit authorization for paid
// automatic discovery. A missing record means discovery is disabled.
type DiscoverySettings struct {
	EditorialProfileID, Provider, Model, UpdatedAt string
	Enabled                                        bool
	DailyLimit, DebounceMinutes                    int
	BatchBudgetCents                               *int64
}

// ProposalBatch is one attention-bounded automatic discovery result.
type ProposalBatch struct {
	ID, EditorialProfileID, Status, WindowStartAt, MaterialSnapshotJSON, IdempotencyKey, ShortageReason string
	Provider, Model                                                                                     *string
	CostCents                                                                                           *int64
	FailureReason                                                                                       *string
	CreatedAt, CompletedAt                                                                              string
}

// CreationProposal is a claim-led candidate direction, independent of a final title.
type CreationProposal struct{ ID, EditorialProfileID, ProposalBatchID, IdeationSessionID, Status, CreationForm, WorkingTitle, ProposedClaim, OwnerClaim, Audience, Rationale, MaterialIDsJSON, HistoryRelationship, CreatedAt, UpdatedAt string }

// CreationHistory stores internal and externally imported work for duplicate checks.
type CreationHistory struct{ ID, EditorialProfileID, Status, CreationForm, Title, CoreClaim, Audience, Content, SourceURL, CreatedAt, UpdatedAt string }

// IdeationSession persists a directed material exploration.
type IdeationSession struct{ ID, EditorialProfileID, Intent, ConstraintsJSON, Status, CreatedAt, UpdatedAt string }

// MaterialDiagnosis captures supports, conflicts, complements, and research gaps for a session.
type MaterialDiagnosis struct{ ID, IdeationSessionID, DiagnosisJSON, MaterialSnapshotJSON, CreatedAt string }

// ResearchNeed blocks or enhances a creation direction until additional learning is available.
type ResearchNeed struct{ ID, CreationProposalID, Severity, Question, Status, ResolutionSourceID, CreatedAt, ResolvedAt string }

// ResearchPlan is an Owner-authorized future research contract; V1 does not execute it.
type ResearchPlan struct {
	ID, ResearchNeedID, Question, Scope, Status, CreatedAt string
	BudgetCents                                            *int64
	OwnerConfirmedAt                                       *string
}

// CreationBrief is the Owner-confirmed contract that authorizes work generation.
type CreationBrief struct {
	ID, CreationProposalID, Status, OwnerClaim, ClaimPlanJSON, MaterialPlanJSON, ResearchNeedIDsJSON, Outline, Style, CreatedAt, UpdatedAt string
	TargetLength                                                                                                                           *int
	ConfirmedAt                                                                                                                            *string
}

// ClaimMapKind identifies responsibility and verification semantics for an expression.
type ClaimMapKind string

// Claim map kinds define responsibility and verification semantics.
const (
	ClaimSource       ClaimMapKind = "source_claim"
	ClaimOwner        ClaimMapKind = "owner_claim"
	ClaimSynthesis    ClaimMapKind = "synthesis_claim"
	ClaimVerifiedFact ClaimMapKind = "verified_fact"
)

// ClaimMap records the responsibility of one expression in a work revision.
type ClaimMap struct {
	ID, WorkRevisionID                                                         string
	Kind                                                                       ClaimMapKind
	Excerpt, KeyPointIDsJSON, OwnerClaim, VerifiedFactSourceIDsJSON, CreatedAt string
}

// ClaimReview is the replacement contract for evidence-only review.
type ClaimReview struct {
	ID, WorkRevisionID, Status, IssuesJSON, CreatedAt string
	Provider, Model, PromptVersion                    *string
	CostCents                                         *int64
}
