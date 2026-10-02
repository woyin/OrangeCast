package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ErrOfflineExpired locks an expired or clock-inconsistent grant without claiming
// that pending browser text has been synchronized.
var ErrOfflineExpired = errors.New("offline authorization expired")

// ErrOfflineRevoked rejects grants from another session, a prior server instance,
// or a device explicitly revoked by its Owner.
var ErrOfflineRevoked = errors.New("offline authorization revoked")

// OfflineSource binds a downloaded source to an immutable transcript snapshot
// and original-audio hash; it cannot silently follow a newer source version.
type OfflineSource struct {
	SourceType  models.SourceType `json:"source_type"`
	SourceID    string            `json:"source_id"`
	SnapshotID  string            `json:"snapshot_id"`
	AudioSHA256 string            `json:"audio_sha256"`
}

// OfflineDevice is a bounded local-use authorization, not a login credential.
// Namespace and epoch isolate a device from other sessions and restored databases.
type OfflineDevice struct {
	Namespace string `json:"namespace"`
	DeviceID  string `json:"device_id"`
	Epoch     string `json:"epoch"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	Revoked   bool   `json:"revoked"`
}

// OfflinePack stores only frozen object pointers and byte metadata. Browser audio
// and unsynchronized text are deliberately absent from server backups.
type OfflinePack struct {
	ID           string          `json:"id"`
	Namespace    string          `json:"namespace"`
	ManifestJSON string          `json:"manifest_json"`
	Sources      []OfflineSource `json:"sources"`
}

// BeginOfflineInstance invalidates all restored/pre-restart grants before serving requests.
func (s *Store) BeginOfflineInstance(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE offline_epoch SET epoch=? WHERE id=1`, uuid.NewString())
	return err
}

// EnableOfflineDevice explicitly replaces this session/device grant with a fresh
// namespace and 24-hour expiry. Missing grants remain disabled by default.
func (s *Store) EnableOfflineDevice(ctx context.Context, session, device string) (*OfflineDevice, error) {
	if len(session) < 16 || uuid.Validate(device) != nil {
		return nil, ErrInvalidEditorialState
	}
	var epoch string
	if err := s.DB.QueryRowContext(ctx, `SELECT epoch FROM offline_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	digest := sha256.Sum256([]byte(session + "\x00" + device + "\x00" + uuid.NewString()))
	ns := hex.EncodeToString(digest[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM offline_devices WHERE session_hash=? AND device_id=?`, session, device); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO offline_devices VALUES(?,?,?,?,?,?,0)`, ns, session, device, epoch, now, now+86400); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ValidateOfflineDevice(ctx, session, ns)
}

// ValidateOfflineDevice checks the exact session, current instance epoch and
// bounded lifetime before any private download or synchronized mutation.
func (s *Store) ValidateOfflineDevice(ctx context.Context, session, namespace string) (*OfflineDevice, error) {
	d := &OfflineDevice{}
	var current string
	err := s.DB.QueryRowContext(ctx, `SELECT d.namespace,d.device_id,d.epoch,d.issued_at,d.expires_at,d.revoked,e.epoch FROM offline_devices d CROSS JOIN offline_epoch e WHERE d.namespace=? AND d.session_hash=?`, namespace, session).Scan(&d.Namespace, &d.DeviceID, &d.Epoch, &d.IssuedAt, &d.ExpiresAt, &d.Revoked, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOfflineRevoked
	}
	if err != nil {
		return nil, err
	}
	if d.Revoked || d.Epoch != current {
		return nil, ErrOfflineRevoked
	}
	now := time.Now().Unix()
	if now < d.IssuedAt || now >= d.ExpiresAt {
		return nil, ErrOfflineExpired
	}
	return d, nil
}

// GetOfflineDevice resolves the current session/device grant and validates it;
// a new login never inherits the old login namespace.
func (s *Store) GetOfflineDevice(ctx context.Context, session, device string) (*OfflineDevice, error) {
	var ns string
	if err := s.DB.QueryRowContext(ctx, `SELECT namespace FROM offline_devices WHERE session_hash=? AND device_id=?`, session, device).Scan(&ns); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOfflineRevoked
		}
		return nil, err
	}
	return s.ValidateOfflineDevice(ctx, session, ns)
}

// RevokeOfflineDevice explicitly invalidates the named session grant. An offline
// browser learns of this only on reconnection or when its local grant expires.
func (s *Store) RevokeOfflineDevice(ctx context.Context, session, namespace string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE offline_devices SET revoked=1 WHERE namespace=? AND session_hash=?`, namespace, session)
	return err
}
func (s *Store) checkOfflineSources(ctx context.Context, sources []OfflineSource) error {
	if len(sources) > 10 {
		return ErrInvalidEditorialState
	}
	for _, source := range sources {
		snap, err := s.FreezeSourceSnapshot(ctx, source.SourceType, source.SourceID)
		if err != nil {
			return err
		}
		if snap.ID != source.SnapshotID || snap.AudioSHA256 != source.AudioSHA256 || snap.Status != models.SnapshotActive {
			return ErrConflict
		}
		if source.AudioSHA256 != "" {
			ea, e := s.GetEvidenceAudio(ctx, source.SourceType, source.SourceID)
			if e != nil {
				return e
			}
			if ea.Status != "ready" || ea.SHA256 != source.AudioSHA256 {
				return ErrConflict
			}
		}
		if _, _, _, err = s.SnapshotContent(ctx, snap.ID); err != nil {
			return err
		}
	}
	return nil
}

// SaveOfflinePack records the selected manifest after source checks, then repeats
// authorization and scope checks inside the write transaction. Device totals are
// bounded to ten sources and 500 MiB; no file body or model task is persisted.
func (s *Store) SaveOfflinePack(ctx context.Context, session, namespace, manifest string, sources []OfflineSource) (string, error) {
	if _, err := s.ValidateOfflineDevice(ctx, session, namespace); err != nil {
		return "", err
	}
	if len(manifest) > 65536 || !json.Valid([]byte(manifest)) {
		return "", ErrInvalidEditorialState
	}
	var m struct {
		PackID string `json:"pack_id"`
		Files  []struct {
			Size int64 `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil || uuid.Validate(m.PackID) != nil {
		return "", ErrInvalidEditorialState
	}
	if err := s.checkOfflineSources(ctx, sources); err != nil {
		return "", err
	}
	var total int64
	for _, f := range m.Files {
		if f.Size < 0 || f.Size > 500<<20 {
			return "", ErrInvalidEditorialState
		}
		total += f.Size
		if total > 500<<20 {
			return "", ErrInvalidEditorialState
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var authorized int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM offline_devices d JOIN offline_epoch e ON e.epoch=d.epoch WHERE d.namespace=? AND d.session_hash=? AND d.revoked=0 AND d.issued_at<=? AND d.expires_at>?`, namespace, session, time.Now().Unix(), time.Now().Unix()).Scan(&authorized); err != nil {
		return "", err
	}
	if authorized != 1 {
		return "", ErrOfflineRevoked
	}
	for _, src := range sources {
		if err = checkOfflineSourceTx(ctx, tx, OfflineOperation{SourceType: src.SourceType, SourceID: src.SourceID, SnapshotID: src.SnapshotID}); err != nil {
			return "", err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT manifest_json,sources_json FROM offline_packs WHERE namespace=?`, namespace)
	if err != nil {
		return "", err
	}
	keys := map[string]bool{}
	for _, src := range sources {
		keys[string(src.SourceType)+"/"+src.SourceID] = true
	}
	for rows.Next() {
		var manifestRaw, sourceRaw string
		if err = rows.Scan(&manifestRaw, &sourceRaw); err != nil {
			rows.Close()
			return "", err
		}
		var old struct {
			Files []struct {
				Size int64 `json:"size"`
			} `json:"files"`
		}
		var oldSources []OfflineSource
		if json.Unmarshal([]byte(manifestRaw), &old) != nil || json.Unmarshal([]byte(sourceRaw), &oldSources) != nil {
			rows.Close()
			return "", ErrConflict
		}
		for _, f := range old.Files {
			total += f.Size
		}
		for _, src := range oldSources {
			keys[string(src.SourceType)+"/"+src.SourceID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if total > 500<<20 || len(keys) > 10 {
		return "", ErrInvalidEditorialState
	}
	raw, _ := json.Marshal(sources)
	if _, err = tx.ExecContext(ctx, `INSERT INTO offline_packs VALUES(?,?,?,?)`, m.PackID, namespace, manifest, string(raw)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return m.PackID, nil
}

// GetOfflinePack rechecks authorization, frozen sources and authoritative article
// evidence before returning metadata; stale packages never become current silently.
func (s *Store) GetOfflinePack(ctx context.Context, session, namespace, id string) (*OfflinePack, error) {
	if _, err := s.ValidateOfflineDevice(ctx, session, namespace); err != nil {
		return nil, err
	}
	p := &OfflinePack{}
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT id,namespace,manifest_json,sources_json FROM offline_packs WHERE id=? AND namespace=?`, id, namespace).Scan(&p.ID, &p.Namespace, &p.ManifestJSON, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &p.Sources); err != nil {
		return nil, err
	}
	if err = s.checkOfflineSources(ctx, p.Sources); err != nil {
		return nil, err
	}
	if err = s.checkOfflineArticleManifest(ctx, p.ManifestJSON); err != nil {
		return nil, err
	}
	return p, nil
}

// ValidateOfflinePackSources makes online status fail closed when any downloaded
// source has changed. Re-enabling the device creates a fresh empty namespace.
func (s *Store) ValidateOfflinePackSources(ctx context.Context, session, namespace string) error {
	if _, err := s.ValidateOfflineDevice(ctx, session, namespace); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sources_json,manifest_json FROM offline_packs WHERE namespace=?`, namespace)
	if err != nil {
		return err
	}
	all := [][]OfflineSource{}
	manifests := []string{}
	for rows.Next() {
		var raw, manifest string
		if err = rows.Scan(&raw, &manifest); err != nil {
			rows.Close()
			return err
		}
		var sources []OfflineSource
		if err = json.Unmarshal([]byte(raw), &sources); err != nil {
			rows.Close()
			return err
		}
		all = append(all, sources)
		manifests = append(manifests, manifest)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i, sources := range all {
		if err = s.checkOfflineSources(ctx, sources); err != nil {
			return ErrConflict
		}
		if err = s.checkOfflineArticleManifest(ctx, manifests[i]); err != nil {
			return ErrConflict
		}
	}
	return nil
}

// DeleteOfflinePacks clears only download authorization metadata; already synced
// Owner text and immutable idempotency receipts are deliberately retained.
func (s *Store) DeleteOfflinePacks(ctx context.Context, session, namespace string) error {
	if _, err := s.ValidateOfflineDevice(ctx, session, namespace); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM offline_packs WHERE namespace=?`, namespace)
	return err
}

// Article-only packs have no direct source entries. Their frozen article version
// must still satisfy current authoritative evidence and permission checks.
func (s *Store) checkOfflineArticleManifest(ctx context.Context, raw string) error {
	var m struct {
		Files []struct {
			Kind     string `json:"kind"`
			ObjectID string `json:"object_id"`
			Revision int    `json:"revision"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ErrConflict
	}
	for _, f := range m.Files {
		if f.Kind != "article" {
			continue
		}
		if f.ObjectID == "" || f.Revision < 1 {
			return ErrConflict
		}
		rev, err := s.GetKnowledgeRevision(ctx, f.ObjectID, f.Revision)
		if err != nil {
			return ErrConflict
		}
		if !rev.Passed || rev.EvidenceStatus != "valid" {
			return ErrConflict
		}
		article, err := s.GetKnowledgeArticle(ctx, f.ObjectID)
		if err != nil {
			return ErrConflict
		}
		state, _, err := s.KnowledgeEvidenceState(ctx, article, rev)
		if err != nil || state != "valid" {
			return ErrConflict
		}
	}
	return nil
}
