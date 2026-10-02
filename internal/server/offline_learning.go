package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

const offlineMaxBytes int64 = 500 << 20

// OfflineFile contains byte identity and immutable object pointers, never a
// credential or a private recording path. Text is regenerated from revisions.
type OfflineFile struct {
	Title            string            `json:"title"`
	ID               string            `json:"id"`
	URL              string            `json:"url"`
	Size             int64             `json:"size"`
	SHA256           string            `json:"sha256"`
	Kind             string            `json:"kind"`
	SourceType       models.SourceType `json:"source_type,omitempty"`
	SourceID         string            `json:"source_id,omitempty"`
	SnapshotID       string            `json:"snapshot_id,omitempty"`
	SnapshotVersion  int               `json:"snapshot_version,omitempty"`
	AudioSHA         string            `json:"audio_sha,omitempty"`
	ObjectID         string            `json:"object_id,omitempty"`
	ProgressRevision int64             `json:"progress_revision,omitempty"`
	Revision         int               `json:"revision,omitempty"`
	ContentType      string            `json:"content_type"`
}

// OfflineManifest freezes the selected byte identities and excerpt anchors under
// one device/session grant. It contains neither credentials nor downloaded bodies.
type OfflineManifest struct {
	OwnerSessionNamespace string           `json:"owner_session_namespace"`
	SchemaVersion         int              `json:"schema_version"`
	Namespace             string           `json:"namespace"`
	DeviceID              string           `json:"device_id"`
	Epoch                 string           `json:"epoch"`
	IssuedAt              int64            `json:"issued_at"`
	ExpiresAt             int64            `json:"expires_at"`
	PackID                string           `json:"pack_id"`
	Excerpts              []map[string]any `json:"excerpts"`
	Files                 []OfflineFile    `json:"files"`
}

func (srv *Server) handleOfflineShell(w http.ResponseWriter, r *http.Request) {
	srv.serveOfflineStatic(w, r, "offline-shell.html", "text/html; charset=utf-8")
}
func (srv *Server) handleOfflineWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Service-Worker-Allowed", "/")
	srv.serveOfflineStatic(w, r, "sw.js", "text/javascript; charset=utf-8")
}
func (srv *Server) serveOfflineStatic(w http.ResponseWriter, r *http.Request, name, contentType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := staticFS.ReadFile("static/" + name)
	if err == nil && name == "sw.js" {
		paths := []string{"/static/offline-shell.html", "/static/offline-core.js", "/static/offline-storage.js", "/static/offline-packs.js", "/static/offline-operations.js", "/static/offline-shell.js"}
		assets := []map[string]string{}
		h := sha256.New()
		_, _ = h.Write(body)
		for _, p := range paths {
			b, e := staticFS.ReadFile(strings.TrimPrefix(p, "/"))
			if e != nil {
				http.Error(w, "offline assets unavailable", 500)
				return
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(b))
			assets = append(assets, map[string]string{"url": p, "sha256": hash})
			_, _ = h.Write(b)
		}
		manifest, _ := json.Marshal(assets)
		version, _ := json.Marshal("cwp-static-offline-" + fmt.Sprintf("%x", h.Sum(nil)))
		body = []byte(strings.ReplaceAll(strings.ReplaceAll(string(body), "__CWP_OFFLINE_ASSETS__", string(manifest)), "__CWP_OFFLINE_VERSION__", string(version)))
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func offlineError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_request"
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, os.ErrNotExist):
		status, code = 404, "material_unavailable"
	case errors.Is(err, store.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, store.ErrOfflineExpired):
		status, code = 410, "offline_expired"
	case errors.Is(err, store.ErrOfflineRevoked):
		status, code = 403, "offline_revoked"
	case errors.Is(err, store.ErrSnapshotInvalidated):
		status, code = 409, "material_unavailable"
	}
	writeJSON(w, status, map[string]any{"error": code, "message": "离线授权或材料不可用；保留未同步文字并重新核对。"})
}
func decodeOffline(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		offlineError(w, err)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		offlineError(w, fmt.Errorf("multiple bodies"))
		return false
	}
	return true
}

func (srv *Server) handleOfflineLearning(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session := auth.PrivateSessionID(r)
	if _, ok := auth.UserIDFromContext(r.Context()); !ok || session == "" {
		w.WriteHeader(401)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/offline/")
	if action == "catalog" && r.Method == http.MethodGet {
		srv.handleOfflineCatalog(w, r)
		return
	}
	if action == "drafts" && r.Method == http.MethodGet {
		drafts, err := srv.store.ListOfflineOrganizingDrafts(r.Context())
		if err != nil {
			offlineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"drafts": drafts})
		return
	}
	if action == "session" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"csrf_token": auth.CSRFValue(r), "session_namespace": session})
		return
	}
	if action == "file" {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		srv.handleOfflineFile(w, r, session)
		return
	}
	if action == "status" {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		device, err := srv.store.GetOfflineDevice(r.Context(), session, r.URL.Query().Get("device_id"))
		if err != nil {
			offlineError(w, err)
			return
		}
		if _, err = srv.store.ValidateOfflineDevice(r.Context(), session, device.Namespace); err != nil {
			offlineError(w, err)
			return
		}
		if err = srv.store.ValidateOfflinePackSources(r.Context(), session, device.Namespace); err != nil {
			offlineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schema_version": 1, "authorization": device, "csrf_token": auth.CSRFValue(r)})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	switch action {
	case "enable":
		var in struct {
			DeviceID string `json:"device_id"`
		}
		if !decodeOffline(w, r, &in) {
			return
		}
		d, err := srv.store.EnableOfflineDevice(r.Context(), session, in.DeviceID)
		if err != nil {
			offlineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"schema_version": 1, "authorization": d, "csrf_token": auth.CSRFValue(r)})
	case "clear-downloads":
		var in struct {
			DeviceID string `json:"device_id"`
		}
		if !decodeOffline(w, r, &in) {
			return
		}
		d, err := srv.store.GetOfflineDevice(r.Context(), session, in.DeviceID)
		if err == nil {
			err = srv.store.DeleteOfflinePacks(r.Context(), session, d.Namespace)
		}
		if err != nil {
			offlineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"cleared": true})
	case "revoke":
		var in struct {
			DeviceID string `json:"device_id"`
		}
		if !decodeOffline(w, r, &in) {
			return
		}
		d, err := srv.store.GetOfflineDevice(r.Context(), session, in.DeviceID)
		if err == nil {
			err = srv.store.RevokeOfflineDevice(r.Context(), session, d.Namespace)
		}
		if err != nil {
			offlineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"revoked": true})
	case "manifest":
		srv.handleOfflineManifest(w, r, session)
	case "sync":
		srv.handleOfflineSync(w, r, session)
	default:
		http.NotFound(w, r)
	}
}

func (srv *Server) handleOfflineManifest(w http.ResponseWriter, r *http.Request, session string) {
	var in struct {
		DeviceID string `json:"device_id"`
		Sources  []struct {
			SourceType models.SourceType `json:"source_type"`
			SourceID   string            `json:"source_id"`
		} `json:"sources"`
		ArticleIDs []string `json:"article_ids"`
		ExcerptIDs []string `json:"excerpt_ids"`
	}
	if !decodeOffline(w, r, &in) {
		return
	}
	if len(in.Sources) > 10 || len(in.Sources)+len(in.ArticleIDs)+len(in.ExcerptIDs) > 100 {
		offlineError(w, fmt.Errorf("scope limit"))
		return
	}
	d, err := srv.store.GetOfflineDevice(r.Context(), session, in.DeviceID)
	if err != nil {
		offlineError(w, err)
		return
	}
	d, err = srv.store.ValidateOfflineDevice(r.Context(), session, d.Namespace)
	if err != nil {
		offlineError(w, err)
		return
	}
	m := OfflineManifest{OwnerSessionNamespace: session, SchemaVersion: 1, Namespace: d.Namespace, DeviceID: d.DeviceID, Epoch: d.Epoch, IssuedAt: d.IssuedAt, ExpiresAt: d.ExpiresAt, PackID: uuid.NewString(), Files: []OfflineFile{}}
	sources := []store.OfflineSource{}
	seen := map[string]bool{}
	var total int64
	add := func(f OfflineFile) error {
		if len(m.Files) >= 100 {
			return fmt.Errorf("100 files limit")
		}
		f.ID = uuid.NewString()
		f.URL = "/api/offline/file?" + url.Values{"device_id": {d.DeviceID}, "pack_id": {m.PackID}, "file_id": {f.ID}}.Encode()
		size, hash, e := srv.offlineFileIdentity(r.Context(), f)
		if e != nil {
			return e
		}
		f.Size, f.SHA256 = size, hash
		total += size
		if total > offlineMaxBytes {
			return fmt.Errorf("500MiB limit")
		}
		m.Files = append(m.Files, f)
		return nil
	}
	for _, s := range in.Sources {
		key := string(s.SourceType) + ":" + s.SourceID
		if seen[key] {
			offlineError(w, fmt.Errorf("duplicate source"))
			return
		}
		seen[key] = true
		if s.SourceType != models.SourceEpisode && s.SourceType != models.SourceUpload {
			offlineError(w, fmt.Errorf("local audio sources only"))
			return
		}
		snap, e := srv.store.FreezeSourceSnapshot(r.Context(), s.SourceType, s.SourceID)
		if e != nil {
			offlineError(w, e)
			return
		}
		base := OfflineFile{SourceType: s.SourceType, SourceID: s.SourceID, SnapshotID: snap.ID, AudioSHA: snap.AudioSHA256, SnapshotVersion: snap.ContentVersion, Title: snap.Title}
		if progress, e := srv.store.GetListeningProgressMode(r.Context(), s.SourceType, s.SourceID, "original"); e == nil {
			base.ProgressRevision = progress.Revision
		}
		base.Kind, base.ContentType = "audio", "audio/mpeg"
		if ev, e := srv.store.GetEvidenceAudio(r.Context(), s.SourceType, s.SourceID); e == nil && ev.Format == "wav" {
			base.ContentType = "audio/wav"
		}
		if e = add(base); e != nil {
			offlineError(w, e)
			return
		}
		base.Kind, base.ContentType = "transcript", "application/json"
		if e = add(base); e != nil {
			offlineError(w, e)
			return
		}
		notes, e := srv.store.ListOwnerNotes(r.Context(), s.SourceType, s.SourceID)
		if e != nil {
			offlineError(w, e)
			return
		}
		for _, n := range notes {
			nf := base
			nf.Kind = "note"
			nf.ObjectID = n.ID
			nf.Revision = n.Revision
			if e = add(nf); e != nil {
				offlineError(w, e)
				return
			}
		}
		sources = append(sources, store.OfflineSource{SourceType: s.SourceType, SourceID: s.SourceID, SnapshotID: snap.ID, AudioSHA256: snap.AudioSHA256})
	}
	for _, id := range in.ExcerptIDs {
		ex, e := srv.store.GetLearningExcerpt(r.Context(), id)
		if e != nil {
			offlineError(w, e)
			return
		}
		if _, e = srv.store.CheckLearningExcerpt(r.Context(), ex); e != nil {
			offlineError(w, e)
			return
		}
		if !seen[string(ex.SourceType)+":"+ex.SourceID] {
			offlineError(w, store.ErrConflict)
			return
		}
		var member *OfflineFile
		for i := range m.Files {
			if m.Files[i].Kind == "audio" && m.Files[i].SourceType == ex.SourceType && m.Files[i].SourceID == ex.SourceID {
				member = &m.Files[i]
				break
			}
		}
		if member == nil || member.SnapshotID != ex.SnapshotID || member.AudioSHA != ex.AudioSHA256 {
			offlineError(w, store.ErrConflict)
			return
		}
		var revision int64
		if p, e := srv.store.GetLearningExcerptProgress(r.Context(), ex.SourceType, ex.SourceID, ex.ID); e == nil {
			revision = p.Revision
		}
		m.Excerpts = append(m.Excerpts, map[string]any{"excerpt_id": ex.ID, "source_type": ex.SourceType, "source_id": ex.SourceID, "snapshot_id": ex.SnapshotID, "snapshot_version": member.SnapshotVersion, "audio_sha256": ex.AudioSHA256, "segment_ids": ex.SegmentIDs, "start_seconds": ex.StartSeconds, "end_seconds": ex.EndSeconds, "progress_revision": revision})
	}
	for _, id := range in.ArticleIDs {
		article, e := srv.store.GetKnowledgeArticle(r.Context(), id)
		if e != nil || article.PassedRevision < 1 {
			if e == nil {
				e = store.ErrNotFound
			}
			offlineError(w, e)
			return
		}
		if e = add(OfflineFile{Kind: "article", Title: article.Title, ObjectID: id, Revision: article.PassedRevision, ContentType: "application/json"}); e != nil {
			offlineError(w, e)
			return
		}
	}
	body, err := json.Marshal(m)
	if err == nil {
		_, err = srv.store.SaveOfflinePack(r.Context(), session, d.Namespace, string(body), sources)
	}
	if err != nil {
		offlineError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (srv *Server) offlineAudio(ctx context.Context, f OfflineFile) (*os.File, error) {
	snap, err := srv.store.GetSourceSnapshot(ctx, f.SnapshotID)
	if err != nil {
		return nil, err
	}
	if snap.Status == models.SnapshotPurged || snap.SourceType != f.SourceType || snap.SourceID != f.SourceID || snap.AudioSHA256 != f.AudioSHA {
		return nil, store.ErrSnapshotInvalidated
	}
	ev, err := srv.store.GetEvidenceAudio(ctx, f.SourceType, f.SourceID)
	if err != nil {
		return nil, err
	}
	if ev.Status != "ready" || ev.SHA256 != f.AudioSHA || len(ev.SHA256) != 64 {
		return nil, store.ErrConflict
	}
	root, err := filepath.EvalSymlinks(srv.cfg.EvidenceDir)
	if err != nil {
		return nil, err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, ev.RelPath))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(ev.RelPath) {
		return nil, store.ErrNotFound
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != ev.SizeBytes || info.Size() > offlineMaxBytes {
		file.Close()
		return nil, store.ErrConflict
	}
	return file, nil
}
func (srv *Server) offlineText(ctx context.Context, f OfflineFile) ([]byte, error) {
	switch f.Kind {
	case "transcript":
		snap, segments, docs, err := srv.store.SnapshotContent(ctx, f.SnapshotID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"snapshot": snap, "segments": segments, "document_segments": docs})
	case "note":
		current, err := srv.store.GetOwnerNote(ctx, f.ObjectID)
		if err != nil {
			return nil, err
		}
		if current.SourceType != string(f.SourceType) || current.SourceID != f.SourceID {
			return nil, store.ErrConflict
		}
		history, err := srv.store.ListOwnerNoteRevisions(ctx, f.ObjectID)
		if err != nil {
			return nil, err
		}
		for _, n := range history {
			if n.Revision == f.Revision {
				return json.Marshal(n)
			}
		}
		return nil, store.ErrNotFound
	case "article":
		rev, err := srv.store.GetKnowledgeRevision(ctx, f.ObjectID, f.Revision)
		if err != nil {
			return nil, err
		}
		if !rev.Passed || rev.EvidenceStatus != "valid" {
			return nil, store.ErrConflict
		}
		article, err := srv.store.GetKnowledgeArticle(ctx, f.ObjectID)
		if err != nil {
			return nil, err
		}
		state, _, err := srv.store.KnowledgeEvidenceState(ctx, article, rev)
		if err != nil {
			return nil, err
		}
		if state != "valid" {
			return nil, store.ErrConflict
		}
		return json.Marshal(map[string]any{"article_id": rev.ArticleID, "revision": rev.Revision, "title": rev.Title, "blocks": json.RawMessage(rev.BlocksJSON), "content_hash": rev.ContentHash, "passed": true})
	default:
		return nil, store.ErrInvalidEditorialState
	}
}
func (srv *Server) offlineFileIdentity(ctx context.Context, f OfflineFile) (int64, string, error) {
	h := sha256.New()
	var size int64
	if f.Kind == "audio" {
		file, err := srv.offlineAudio(ctx, f)
		if err != nil {
			return 0, "", err
		}
		defer file.Close()
		size, err = io.Copy(h, io.LimitReader(file, offlineMaxBytes+1))
		if err != nil {
			return 0, "", err
		}
		if fmt.Sprintf("%x", h.Sum(nil)) != f.AudioSHA {
			return 0, "", store.ErrConflict
		}
	} else {
		b, err := srv.offlineText(ctx, f)
		if err != nil {
			return 0, "", err
		}
		size = int64(len(b))
		_, _ = h.Write(b)
	}
	return size, fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Spooling fixes the exact bytes before response headers. A source file changed
// between manifest validation and download cannot be served under the old hash.
func (srv *Server) offlineVerifiedAudio(ctx context.Context, f OfflineFile) (*os.File, error) {
	source, err := srv.offlineAudio(ctx, f)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	tmp, err := os.CreateTemp(srv.cfg.TempDir, "cwp-offline-download-*")
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*os.File, error) { tmp.Close(); os.Remove(tmp.Name()); return nil, e }
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(source, offlineMaxBytes+1))
	if err != nil {
		return fail(err)
	}
	if size != f.Size || fmt.Sprintf("%x", h.Sum(nil)) != f.SHA256 {
		return fail(store.ErrConflict)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return tmp, nil
}
func (srv *Server) handleOfflineFile(w http.ResponseWriter, r *http.Request, session string) {
	d, err := srv.store.GetOfflineDevice(r.Context(), session, r.URL.Query().Get("device_id"))
	if err != nil {
		offlineError(w, err)
		return
	}
	pack, err := srv.store.GetOfflinePack(r.Context(), session, d.Namespace, r.URL.Query().Get("pack_id"))
	if err != nil {
		offlineError(w, err)
		return
	}
	var m OfflineManifest
	if json.Unmarshal([]byte(pack.ManifestJSON), &m) != nil {
		offlineError(w, store.ErrConflict)
		return
	}
	for _, f := range m.Files {
		if f.ID != r.URL.Query().Get("file_id") {
			continue
		}
		var reader io.ReadSeeker
		if f.Kind == "audio" {
			file, e := srv.offlineVerifiedAudio(r.Context(), f)
			if e != nil {
				offlineError(w, e)
				return
			}
			defer func() { file.Close(); os.Remove(file.Name()) }()
			reader = file
		} else {
			body, e := srv.offlineText(r.Context(), f)
			if e != nil {
				offlineError(w, e)
				return
			}
			if int64(len(body)) != f.Size || fmt.Sprintf("%x", sha256.Sum256(body)) != f.SHA256 {
				offlineError(w, store.ErrConflict)
				return
			}
			reader = bytes.NewReader(body)
		}
		if _, err = srv.store.GetOfflinePack(r.Context(), session, d.Namespace, m.PackID); err != nil {
			offlineError(w, err)
			return
		}
		w.Header().Set("Content-Type", f.ContentType)
		w.Header().Set("X-Content-SHA256", f.SHA256)
		http.ServeContent(w, r, f.ID, time.Time{}, reader)
		return
	}
	http.NotFound(w, r)
}
func (srv *Server) handleOfflineSync(w http.ResponseWriter, r *http.Request, session string) {
	var in struct {
		DeviceID   string                   `json:"device_id"`
		Namespace  string                   `json:"namespace"`
		Epoch      string                   `json:"epoch"`
		Operations []store.OfflineOperation `json:"operations"`
	}
	if !decodeOffline(w, r, &in) {
		return
	}
	if len(in.Operations) > 100 {
		offlineError(w, store.ErrInvalidEditorialState)
		return
	}
	d, err := srv.store.GetOfflineDevice(r.Context(), session, in.DeviceID)
	if err == nil {
		d, err = srv.store.ValidateOfflineDevice(r.Context(), session, d.Namespace)
	}
	if err != nil {
		offlineError(w, err)
		return
	}
	if d.Namespace != in.Namespace || d.Epoch != in.Epoch {
		offlineError(w, store.ErrOfflineRevoked)
		return
	}
	results := []map[string]any{}
	responseStatus := http.StatusOK
	for _, op := range in.Operations {
		receipt, e := srv.store.ApplyOfflineOperation(r.Context(), session, d.Namespace, op)
		if e != nil {
			status := "unavailable"
			if errors.Is(e, store.ErrConflict) {
				status = "conflict"
				responseStatus = http.StatusConflict
			}
			results = append(results, map[string]any{"uuid": op.UUID, "status": status})
			continue
		}
		results = append(results, map[string]any{"uuid": op.UUID, "status": "ack", "receipt": receipt})
	}
	writeJSON(w, responseStatus, map[string]any{"results": results})
}

// The selection UI receives titles and availability, rather than asking the
// learner to copy internal IDs. Bodies and audio are downloaded only by POST.
func (srv *Server) handleOfflineCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := srv.store.ListKnowledgeSearchSources(r.Context())
	if err != nil {
		offlineError(w, err)
		return
	}
	sources := []map[string]any{}
	articles := []map[string]any{}
	excerpts := []models.ListeningQueueItem{}
	for _, s := range list {
		kind := models.SourceType(s.SourceType)
		if kind != models.SourceEpisode && kind != models.SourceUpload {
			continue
		}
		ev, e := srv.store.GetEvidenceAudio(r.Context(), kind, s.SourceID)
		if e != nil || ev.Status != "ready" || len(ev.SHA256) != 64 || ev.SizeBytes <= 0 {
			continue
		}
		if _, e = srv.store.GetCurrentVersion(r.Context(), kind, s.SourceID, store.KindTranscript); e != nil {
			continue
		}
		sources = append(sources, map[string]any{"source_type": kind, "source_id": s.SourceID, "title": s.Title, "size": ev.SizeBytes})
		if len(sources) >= 100 {
			break
		}
	}
	available, e := srv.store.ListKnowledgeArticles(r.Context())
	if e != nil {
		offlineError(w, e)
		return
	}
	for _, a := range available {
		if a.PassedRevision < 1 {
			continue
		}
		rev, e := srv.store.GetKnowledgeRevision(r.Context(), a.ID, a.PassedRevision)
		if e != nil || !rev.Passed {
			continue
		}
		state, _, e := srv.store.KnowledgeEvidenceState(r.Context(), a, rev)
		if e != nil || state != "valid" {
			continue
		}
		articles = append(articles, map[string]any{"article_id": a.ID, "title": rev.Title, "revision": rev.Revision})
	}
	if q, e := srv.store.GetListeningQueue(r.Context()); e == nil {
		for _, item := range q.Items {
			if item.Mode == "excerpt" && item.Available {
				excerpts = append(excerpts, item)
				if len(excerpts) >= 100 {
					break
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"sources": sources, "articles": articles, "excerpts": excerpts})
}
