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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/store"
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
func Create(ctx context.Context, s *store.Store, evidenceDir, destFile string) (Manifest, error) {
	var m Manifest
	if err := os.MkdirAll(filepath.Dir(destFile), 0o755); err != nil {
		return m, err
	}

	// 1) 一致性数据库快照 → 临时文件
	tmp, err := os.MkdirTemp("", "cwp-backup-*")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	dbSnapshot := filepath.Join(tmp, dbFileName)
	if err := store.ConsistencyBackup(ctx, s.DB, dbSnapshot); err != nil {
		return m, fmt.Errorf("数据库快照: %w", err)
	}
	dbSHA, err := fileSHA256(dbSnapshot)
	if err != nil {
		return m, err
	}

	// 2) 收集证据文件清单
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
		sha, err := fileSHA256(path)
		if err != nil {
			return err
		}
		entries = append(entries, EvidenceEntry{RelPath: filepath.ToSlash(rel), SHA256: sha, SizeBytes: fi.Size()})
		return nil
	}); err != nil {
		return m, fmt.Errorf("扫描证据目录: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })

	m = Manifest{
		Format: ManifestFormat, Version: ManifestVersion,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		DBFile:    dbFileName, DBSHA256: dbSHA,
		Evidence: entries,
	}

	// 3) 打包 tar.gz：manifest + db + evidence
	f, err := os.Create(destFile)
	if err != nil {
		return m, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	writeFile := func(name, path string, size int64) error {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close() // 函数内立即返回，不跨迭代累积 FD
		_, err = io.Copy(tw, src)
		return err
	}

	manifestJSON, _ := json.MarshalIndent(m, "", "  ")
	{
		hdr := &tar.Header{Name: manifestFileName, Mode: 0o644, Size: int64(len(manifestJSON)), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return m, err
		}
		if _, err := tw.Write(manifestJSON); err != nil {
			return m, err
		}
	}
	dbFi, err := os.Stat(dbSnapshot)
	if err != nil {
		return m, err
	}
	if err := writeFile(m.DBFile, dbSnapshot, dbFi.Size()); err != nil {
		return m, err
	}
	for _, e := range entries {
		path := filepath.Join(evidenceDir, filepath.FromSlash(e.RelPath))
		fi, err := os.Stat(path)
		if err != nil {
			return m, err
		}
		if err := writeFile(evidenceDirPrefix+e.RelPath, path, fi.Size()); err != nil {
			return m, err
		}
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	if err := gz.Close(); err != nil {
		return m, err
	}
	return m, nil
}

// Restore 从备份包恢复到目标数据目录。
// 安全要求：目标目录必须为空（不含 cloudwisepod.db），或 force=true 显式覆盖。
// 恢复前校验 manifest 格式版本、DB 哈希与证据文件哈希。
func Restore(ctx context.Context, backupPath, targetDataDir string, force bool) (Manifest, error) {
	var m Manifest
	f, err := os.Open(backupPath)
	if err != nil {
		return m, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return m, fmt.Errorf("打开备份包: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	// 目标检查
	targetDB := filepath.Join(targetDataDir, dbFileName)
	if !force {
		if _, err := os.Stat(targetDB); err == nil {
			return m, fmt.Errorf("目标目录已存在 %s；使用 --force 显式覆盖", targetDB)
		}
	}
	if err := os.MkdirAll(targetDataDir, 0o755); err != nil {
		return m, err
	}

	// 解包到临时目录，先校验再落地（避免半成品污染目标）
	tmp, err := os.MkdirTemp("", "cwp-restore-*")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)

	var manifestData []byte
	dbPath := ""
	extractedEvidence := map[string]string{} // rel_path → tmp path
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, err
		}
		if hdr.Name == manifestFileName {
			manifestData, err = io.ReadAll(tr)
			if err != nil {
				return m, err
			}
			continue
		}
		if hdr.Name == dbFileName {
			dbPath = filepath.Join(tmp, dbFileName)
			if err := copyFromTar(tr, dbPath); err != nil {
				return m, err
			}
			continue
		}
		if strings.HasPrefix(hdr.Name, evidenceDirPrefix) {
			rel := strings.TrimPrefix(hdr.Name, evidenceDirPrefix)
			dest := filepath.Join(tmp, "evidence", filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return m, err
			}
			if err := copyFromTar(tr, dest); err != nil {
				return m, err
			}
			extractedEvidence[rel] = dest
			continue
		}
	}

	if len(manifestData) == 0 || dbPath == "" {
		return m, fmt.Errorf("备份包缺少 manifest 或数据库文件")
	}
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return m, fmt.Errorf("解析 manifest: %w", err)
	}
	if m.Format != ManifestFormat || m.Version != ManifestVersion {
		return m, fmt.Errorf("不支持的备份格式: %s v%d", m.Format, m.Version)
	}

	// 校验 DB 哈希
	gotSHA, err := fileSHA256(dbPath)
	if err != nil {
		return m, err
	}
	if gotSHA != m.DBSHA256 {
		return m, fmt.Errorf("数据库哈希校验失败：备份 %s，实际 %s", m.DBSHA256, gotSHA)
	}
	// 校验证据哈希
	for _, e := range m.Evidence {
		p, ok := extractedEvidence[e.RelPath]
		if !ok {
			return m, fmt.Errorf("备份包缺少证据文件 %s", e.RelPath)
		}
		sha, err := fileSHA256(p)
		if err != nil {
			return m, err
		}
		if sha != e.SHA256 {
			return m, fmt.Errorf("证据文件 %s 哈希校验失败", e.RelPath)
		}
	}

	// 全部校验通过后落地
	if err := os.Rename(dbPath, targetDB); err != nil {
		return m, err
	}
	evDir := filepath.Join(targetDataDir, "evidence")
	for _, e := range m.Evidence {
		src := extractedEvidence[e.RelPath]
		dst := filepath.Join(evDir, filepath.FromSlash(e.RelPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return m, err
		}
		if err := os.Rename(src, dst); err != nil {
			return m, err
		}
	}
	return m, nil
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

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
