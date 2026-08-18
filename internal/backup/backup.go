// Package backup 提供一致性备份与恢复（ADR-0010 / Roadmap Phase 6）。
//
// 备份包结构（tar.gz）：
//
//	manifest.json    —— 格式版本、数据库哈希、证据文件清单（不含任何密钥）
//	cloudwisepod.db  —— SQLite 一致性快照（VACUUM INTO，非直接复制运行中文件）
//	evidence/…       —— 全部 EvidenceAudio（原始 rel_path）
//
//	Narration（DATA_DIR/narrations）是可重新生成的衍生层产物，不进备份包（ADR-0019 R2）；
//	全新实例恢复后按需重合成，备份只保核心证据。
//
// 恢复：校验 manifest 格式版本与文件哈希；目标目录为空或显式 --force 才允许覆盖。
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/store"
)

// archiveTWriter 描述 Create 写入归档所需的最小 tar 写入能力，便于测试注入伪造失败。
type archiveTWriter interface {
	WriteHeader(*tar.Header) error
	Write([]byte) (int, error)
	Close() error
}

// archiveGWriter 描述 Create 需要的 gzip 层能力（Write + Close），便于测试注入伪造 Close 失败。
type archiveGWriter interface {
	Write([]byte) (int, error)
	Close() error
}

// 以下工厂变量仅作为测试缝（test seam），通过替换实现来模拟归档写入失败分支。
// 默认实现使用标准库，行为与重构前完全一致；生产代码不修改它们。
var (
	// newGzipWriter 创建 gzip 写入器。
	newGzipWriter = func(w io.Writer) archiveGWriter { return gzip.NewWriter(w) }
	// newTarWriter 创建 tar 写入器。
	newTarWriter = func(w io.Writer) archiveTWriter { return tar.NewWriter(w) }
	// openArchiveSrc 打开将被写入归档的源文件。
	openArchiveSrc = func(p string) (io.ReadCloser, error) { return os.Open(p) }
)

// ManifestFormat 备份包格式标识与版本。
const (
	ManifestFormat    = "cloudwisepod-backup"
	ManifestVersion   = 1
	manifestFileName  = "manifest.json"
	dbFileName        = "cloudwisepod.db"
	evidenceDirPrefix = "evidence/"
)

// EvidenceEntry 证据文件清单项。
type EvidenceEntry struct {
	RelPath   string `json:"rel_path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Manifest 备份包清单（不含密钥/Session secret）。
type Manifest struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	CreatedAt  string          `json:"created_at"`
	AppVersion string          `json:"app_version"`
	DBFile     string          `json:"db_file"`
	DBSHA256   string          `json:"db_sha256"`
	Evidence   []EvidenceEntry `json:"evidence"`
}

// Create 生成一致性备份包到 destFile（.tar.gz）。
// 数据库用 ConsistencyBackup（VACUUM INTO 快照）；证据文件按哈希清单打包。
func collectEvidenceEntries(evidenceDir string) ([]EvidenceEntry, error) {
	var entries []EvidenceEntry
	if err := filepath.Walk(evidenceDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(evidenceDir, path)
		if err != nil {
			return err
		}
		sha, err := filehash.SHA256(path)
		if err != nil {
			return err
		}
		entries = append(entries, EvidenceEntry{RelPath: filepath.ToSlash(rel), SHA256: sha, SizeBytes: fi.Size()})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("扫描证据目录: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })
	return entries, nil
}

func writeBackupArchive(destFile, dbSnapshot, evidenceDir string, manifest Manifest) error {
	f, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := newGzipWriter(f)
	tw := newTarWriter(gz)

	writeFile := func(name, path string, size int64) error {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := openArchiveSrc(path)
		if err != nil {
			return err
		}
		defer src.Close() // 函数内立即返回，不跨迭代累积 FD
		_, err = io.Copy(tw, src)
		return err
	}

	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	hdr := &tar.Header{Name: manifestFileName, Mode: 0o644, Size: int64(len(manifestJSON)), ModTime: time.Unix(0, 0)}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return err
	}
	dbFi, err := os.Stat(dbSnapshot)
	if err != nil {
		return err
	}
	if err := writeFile(manifest.DBFile, dbSnapshot, dbFi.Size()); err != nil {
		return err
	}
	for _, e := range manifest.Evidence {
		path := filepath.Join(evidenceDir, filepath.FromSlash(e.RelPath))
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := writeFile(evidenceDirPrefix+e.RelPath, path, fi.Size()); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return nil
}

// Create writes a consistent backup package of the store and evidence directory to destFile and returns its Manifest.
func Create(ctx context.Context, s *store.Store, evidenceDir, destFile string) (Manifest, error) {
	var manifest Manifest
	if err := os.MkdirAll(filepath.Dir(destFile), 0o755); err != nil {
		return manifest, err
	}
	tmp, err := os.MkdirTemp("", "cwp-backup-*")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(tmp)
	dbSnapshot := filepath.Join(tmp, dbFileName)
	if err := store.ConsistencyBackup(ctx, s.DB, dbSnapshot); err != nil {
		return manifest, fmt.Errorf("数据库快照: %w", err)
	}
	dbSHA, err := filehash.SHA256(dbSnapshot)
	if err != nil {
		return manifest, err
	}
	entries, err := collectEvidenceEntries(evidenceDir)
	if err != nil {
		return manifest, err
	}
	manifest = Manifest{Format: ManifestFormat, Version: ManifestVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339), DBFile: dbFileName, DBSHA256: dbSHA, Evidence: entries}
	if err := writeBackupArchive(destFile, dbSnapshot, evidenceDir, manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// Restore 从备份包恢复到目标数据目录。
// 安全要求：目标目录必须为空（不含 cloudwisepod.db），或 force=true 显式覆盖。
// 恢复前校验 manifest 格式版本、DB 哈希与证据文件哈希。
type extractedArchive struct {
	manifestData      []byte
	dbPath            string
	extractedEvidence map[string]string
}

func extractArchive(tr *tar.Reader, tmp string) (extractedArchive, error) {
	archive := extractedArchive{extractedEvidence: map[string]string{}}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return archive, nil
		}
		if err != nil {
			return archive, err
		}
		switch {
		case header.Name == manifestFileName:
			archive.manifestData, err = io.ReadAll(tr)
		case header.Name == dbFileName:
			archive.dbPath = filepath.Join(tmp, dbFileName)
			if err == nil {
				err = copyFromTar(tr, archive.dbPath)
			}
		case strings.HasPrefix(header.Name, evidenceDirPrefix):
			rel := strings.TrimPrefix(header.Name, evidenceDirPrefix)
			dest := filepath.Join(tmp, "evidence", filepath.FromSlash(rel))
			if err == nil {
				err = os.MkdirAll(filepath.Dir(dest), 0o755)
			}
			if err == nil {
				err = copyFromTar(tr, dest)
			}
			archive.extractedEvidence[rel] = dest
		}
		if err != nil {
			return archive, err
		}
	}
}

func validateExtractedArchive(archive extractedArchive) (Manifest, error) {
	var manifest Manifest
	if len(archive.manifestData) == 0 || archive.dbPath == "" {
		return manifest, fmt.Errorf("备份包缺少 manifest 或数据库文件")
	}
	if err := json.Unmarshal(archive.manifestData, &manifest); err != nil {
		return manifest, fmt.Errorf("解析 manifest: %w", err)
	}
	if manifest.Format != ManifestFormat || manifest.Version != ManifestVersion {
		return manifest, fmt.Errorf("不支持的备份格式: %s v%d", manifest.Format, manifest.Version)
	}
	gotSHA, err := filehash.SHA256(archive.dbPath)
	if err != nil {
		return manifest, err
	}
	if gotSHA != manifest.DBSHA256 {
		return manifest, fmt.Errorf("数据库哈希校验失败：备份 %s，实际 %s", manifest.DBSHA256, gotSHA)
	}
	for _, entry := range manifest.Evidence {
		path, ok := archive.extractedEvidence[entry.RelPath]
		if !ok {
			return manifest, fmt.Errorf("备份包缺少证据文件 %s", entry.RelPath)
		}
		sha, err := filehash.SHA256(path)
		if err != nil {
			return manifest, err
		}
		if sha != entry.SHA256 {
			return manifest, fmt.Errorf("证据文件 %s 哈希校验失败", entry.RelPath)
		}
	}
	return manifest, nil
}

func installArchive(archive extractedArchive, manifest Manifest, targetDataDir string) error {
	if err := os.Rename(archive.dbPath, filepath.Join(targetDataDir, dbFileName)); err != nil {
		return err
	}
	evidenceDir := filepath.Join(targetDataDir, "evidence")
	for _, entry := range manifest.Evidence {
		src := archive.extractedEvidence[entry.RelPath]
		dst := filepath.Join(evidenceDir, filepath.FromSlash(entry.RelPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// Restore unpacks a backup package into targetDataDir and verifies it against the embedded Manifest.
func Restore(ctx context.Context, backupPath, targetDataDir string, force bool) (Manifest, error) {
	var manifest Manifest
	f, err := os.Open(backupPath)
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return manifest, fmt.Errorf("打开备份包: %w", err)
	}
	defer gz.Close()
	if !force {
		if _, err := os.Stat(filepath.Join(targetDataDir, dbFileName)); err == nil {
			return manifest, fmt.Errorf("目标目录已存在 %s；使用 --force 显式覆盖", filepath.Join(targetDataDir, dbFileName))
		}
	}
	if err := os.MkdirAll(targetDataDir, 0o755); err != nil {
		return manifest, err
	}
	tmp, err := os.MkdirTemp("", "cwp-restore-*")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(tmp)
	archive, err := extractArchive(tar.NewReader(gz), tmp)
	if err != nil {
		return manifest, err
	}
	manifest, err = validateExtractedArchive(archive)
	if err != nil {
		return manifest, err
	}
	if err := installArchive(archive, manifest, targetDataDir); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func copyFromTar(tr *tar.Reader, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, tr)
	return err
}
