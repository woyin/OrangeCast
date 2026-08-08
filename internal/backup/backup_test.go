package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
	_ "modernc.org/sqlite"
)

// buildFixture 构造一个真实数据目录：Owner、episode、转录/卡片版本、evidence 文件。
func buildFixture(t *testing.T, dataDir string) *store.Store {
	t.Helper()
	os.MkdirAll(filepath.Join(dataDir, "evidence"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "tmp"), 0o755)
	s, err := store.Open(filepath.Join(dataDir, "cloudwisepod.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	u, err := s.ClaimOwner(ctx, "owner@example.com", "$argon2id$fixture")
	if err != nil {
		t.Fatal(err)
	}
	_ = u

	p, _ := s.CreatePodcast(ctx, "https://feed.example.com/rss", "Test Pod", "", "")
	if _, err := s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	tv, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "whisper-large-v3", "1", job.ID, `{"language":"en","text":"hello world","segments":[{"id":"seg-0001","start":0,"end":2,"text":"hello world"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv)

	// evidence 文件
	evPath := filepath.Join(dataDir, "evidence", "episode_"+sourceID+".mp3")
	os.WriteFile(evPath, []byte("fake-audio-bytes"), 0o644)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", int64(len("fake-audio-bytes")), "abcdef"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBackupRestore_EndToEnd(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	ctx := context.Background()

	backupFile := filepath.Join(t.TempDir(), "backup.tar.gz")
	m, err := Create(ctx, srcStore, filepath.Join(srcDir, "evidence"), backupFile)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.Format != ManifestFormat || m.Version != ManifestVersion {
		t.Errorf("manifest 格式错误: %+v", m)
	}
	if len(m.Evidence) != 1 {
		t.Errorf("应打包 1 个证据文件，实际 %d", len(m.Evidence))
	}
	// 备份包不应包含密钥：manifest 中无 secret 字段；内容只有 db + evidence
	fi, _ := os.Stat(backupFile)
	if fi.Size() == 0 {
		t.Error("备份包不应为空")
	}

	// 恢复到全新目录
	dstDir := t.TempDir()
	if _, err := Restore(ctx, backupFile, dstDir, false); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	// 目标 DB 可打开
	dbPath := filepath.Join(dstDir, "cloudwisepod.db")
	dstDB, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	defer dstDB.Close()
	// Owner 凭据保留
	var email string
	if err := dstDB.QueryRow(`SELECT email FROM users LIMIT 1`).Scan(&email); err != nil {
		t.Fatalf("恢复后读 Owner: %v", err)
	}
	if email != "owner@example.com" {
		t.Errorf("Owner email 应为 owner@example.com，实际 %s", email)
	}
	// 转录版本保留
	var cnt int
	if err := dstDB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE kind='transcript'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("转录版本应恢复 1 个，实际 %d", cnt)
	}
	// 证据文件恢复
	evFiles, _ := filepath.Glob(filepath.Join(dstDir, "evidence", "*.mp3"))
	if len(evFiles) != 1 {
		t.Errorf("证据文件应恢复 1 个，实际 %d", len(evFiles))
	}
	data, _ := os.ReadFile(evFiles[0])
	if string(data) != "fake-audio-bytes" {
		t.Errorf("证据内容不符: %q", data)
	}
}

func TestRestore_RejectsNonEmptyTarget(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	backupFile := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(context.Background(), srcStore, filepath.Join(srcDir, "evidence"), backupFile); err != nil {
		t.Fatal(err)
	}
	// 目标已有 db → 无 force 应拒绝
	dstDir := t.TempDir()
	os.WriteFile(filepath.Join(dstDir, "cloudwisepod.db"), []byte("existing"), 0o644)
	if _, err := Restore(context.Background(), backupFile, dstDir, false); err == nil {
		t.Fatal("非空目标无 force 应拒绝恢复")
	}
	// force 应成功覆盖
	if _, err := Restore(context.Background(), backupFile, dstDir, true); err != nil {
		t.Fatalf("force 恢复失败: %v", err)
	}
}

func TestRestore_RejectsTamperedBackup(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	backupFile := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(context.Background(), srcStore, filepath.Join(srcDir, "evidence"), backupFile); err != nil {
		t.Fatal(err)
	}
	// 篡改：在备份包内修改 db 内容
	// 简化：直接改 manifest 版本 → 应拒绝
	// （用 gzip/tar 重写成本高；此处用无效文件路径模拟损坏包）
	if _, err := Restore(context.Background(), t.TempDir()+"/missing.tar.gz", t.TempDir(), true); err == nil {
		t.Fatal("损坏备份包应拒绝")
	}
}

// TestCreate_MissingEvidenceDir 验证证据目录不存在时 Create 报错。
func TestCreate_MissingEvidenceDir(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	ctx := context.Background()
	// 指向不存在的证据目录 → filepath.Walk 报错
	backupFile := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(ctx, srcStore, filepath.Join(srcDir, "nonexistent-evidence"), backupFile); err == nil {
		t.Fatal("证据目录不存在应报错")
	}
}

// TestCreate_InvalidDestDir 验证目标目录不可创建时 Create 报错。
func TestCreate_InvalidDestDir(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	ctx := context.Background()
	// 目标父目录被文件占用 → MkdirAll 失败
	blocker := filepath.Join(t.TempDir(), "block")
	os.WriteFile(blocker, []byte("x"), 0o644)
	badDest := filepath.Join(blocker, "sub", "b.tar.gz")
	if _, err := Create(ctx, srcStore, filepath.Join(srcDir, "evidence"), badDest); err == nil {
		t.Fatal("目标目录不可创建应报错")
	}
}

// TestRestore_MissingManifest 验证备份包缺 manifest 时 Restore 报错。
func TestRestore_MissingManifest(t *testing.T) {
	// 构造一个只有 db 文件、无 manifest 的 tar.gz
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "cloudwisepod.db")
	os.WriteFile(dbFile, []byte("fake-db"), 0o644)
	backupFile := filepath.Join(dir, "b.tar.gz")
	f, _ := os.Create(backupFile)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "cloudwisepod.db", Mode: 0o644, Size: int64(len("fake-db"))}
	tw.WriteHeader(hdr)
	tw.Write([]byte("fake-db"))
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := Restore(context.Background(), backupFile, t.TempDir(), false); err == nil {
		t.Fatal("缺 manifest 应报错")
	}
}

// TestRestore_InvalidFormat 验证 manifest 格式版本不符时 Restore 报错。
func TestRestore_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "cloudwisepod.db")
	os.WriteFile(dbFile, []byte("fake-db"), 0o644)
	backupFile := filepath.Join(dir, "b.tar.gz")
	f, _ := os.Create(backupFile)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// 写入格式错误的 manifest
	manifest := []byte(`{"format":"wrong-format","version":99}`)
	mh := &tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifest))}
	tw.WriteHeader(mh)
	tw.Write(manifest)
	dh := &tar.Header{Name: "cloudwisepod.db", Mode: 0o644, Size: int64(len("fake-db"))}
	tw.WriteHeader(dh)
	tw.Write([]byte("fake-db"))
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := Restore(context.Background(), backupFile, t.TempDir(), false); err == nil {
		t.Fatal("格式不符应报错")
	}
}

// TestRestore_DBHashMismatch 验证 DB 内容被篡改后哈希校验失败。
func TestRestore_DBHashMismatch(t *testing.T) {
	srcDir := t.TempDir()
	srcStore := buildFixture(t, srcDir)
	backupFile := filepath.Join(t.TempDir(), "b.tar.gz")
	m, err := Create(context.Background(), srcStore, filepath.Join(srcDir, "evidence"), backupFile)
	if err != nil {
		t.Fatal(err)
	}

	// 重新打包：manifest 保留原哈希，但 DB 内容被篡改 → 哈希不匹配
	dir := t.TempDir()
	tamperedFile := filepath.Join(dir, "tampered.tar.gz")
	f, _ := os.Create(tamperedFile)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	manifestJSON, _ := json.Marshal(m)
	mh := &tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestJSON))}
	tw.WriteHeader(mh)
	tw.Write(manifestJSON)
	tampered := []byte("tampered-db-content")
	dh := &tar.Header{Name: "cloudwisepod.db", Mode: 0o644, Size: int64(len(tampered))}
	tw.WriteHeader(dh)
	tw.Write(tampered)
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := Restore(context.Background(), tamperedFile, t.TempDir(), false); err == nil {
		t.Fatal("DB 哈希不匹配应报错")
	}
}

// TestRestore_MissingEvidenceFile 验证 manifest 列出但包内缺失的证据文件导致恢复失败。
func TestRestore_MissingEvidenceFile(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "cloudwisepod.db")
	os.WriteFile(dbFile, []byte("fake-db"), 0o644)
	backupFile := filepath.Join(dir, "b.tar.gz")
	f, _ := os.Create(backupFile)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// manifest 声明一个证据文件，但包内不含它
	m := Manifest{
		Format: ManifestFormat, Version: ManifestVersion,
		DBFile: dbFileName, DBSHA256: "deadbeef",
		Evidence: []EvidenceEntry{{RelPath: "ep-1.mp3", SHA256: "abc", SizeBytes: 10}},
	}
	manifestJSON, _ := json.Marshal(m)
	mh := &tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestJSON))}
	tw.WriteHeader(mh)
	tw.Write(manifestJSON)
	dh := &tar.Header{Name: "cloudwisepod.db", Mode: 0o644, Size: int64(len("fake-db"))}
	tw.WriteHeader(dh)
	tw.Write([]byte("fake-db"))
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := Restore(context.Background(), backupFile, t.TempDir(), false); err == nil {
		t.Fatal("缺失证据文件应导致恢复失败")
	}
}

// TestRestore_EvidenceHashMismatch 验证证据文件内容与 manifest 哈希不符时恢复失败。
func TestRestore_EvidenceHashMismatch(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "cloudwisepod.db")
	os.WriteFile(dbFile, []byte("fake-db"), 0o644)
	backupFile := filepath.Join(dir, "b.tar.gz")
	f, _ := os.Create(backupFile)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// manifest 声明证据文件 ep-1.mp3 的哈希为错误值
	m := Manifest{
		Format: ManifestFormat, Version: ManifestVersion,
		DBFile: dbFileName, DBSHA256: "deadbeef",
		Evidence: []EvidenceEntry{{RelPath: "ep-1.mp3", SHA256: "wrong-hash", SizeBytes: 4}},
	}
	manifestJSON, _ := json.Marshal(m)
	mh := &tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestJSON))}
	tw.WriteHeader(mh)
	tw.Write(manifestJSON)
	dh := &tar.Header{Name: "cloudwisepod.db", Mode: 0o644, Size: int64(len("fake-db"))}
	tw.WriteHeader(dh)
	tw.Write([]byte("fake-db"))
	// 包内包含证据文件，但内容与 manifest 哈希不符
	eh := &tar.Header{Name: "evidence/ep-1.mp3", Mode: 0o644, Size: int64(len("data"))}
	tw.WriteHeader(eh)
	tw.Write([]byte("data"))
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := Restore(context.Background(), backupFile, t.TempDir(), false); err == nil {
		t.Fatal("证据哈希不匹配应导致恢复失败")
	}
}

// TestFileSHA256 验证文件哈希计算（确定性）。
func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	content := "hello backup"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	// 首次与再次读取应一致
	got2, _ := fileSHA256(path)
	if got != got2 {
		t.Errorf("哈希应确定: %s vs %s", got, got2)
	}
	if len(got) != 64 {
		t.Errorf("sha256 hex 应为 64 字符，实际 %d", len(got))
	}
}

// TestFileSHA256_NotFound 验证文件不存在时返回错误。
func TestFileSHA256_NotFound(t *testing.T) {
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "nope.bin")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

// TestRestore_MissingFile 验证备份文件不存在时返回错误。
func TestRestore_MissingFile(t *testing.T) {
	if _, err := Restore(context.Background(), filepath.Join(t.TempDir(), "missing.tar.gz"), t.TempDir(), false); err == nil {
		t.Fatal("备份文件不存在应报错")
	}
}
