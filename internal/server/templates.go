package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// StaticFS 暴露静态资源文件系统（供路由挂载）。
func StaticFS() (fs.FS, error) {
	return fs.Sub(staticFS, "static")
}

// Templates 用两阶段渲染：layout 定义骨架，每个页面只覆盖 "content"（和可选 "title"）块。
type Templates struct {
	pages map[string]*template.Template
}

// NewTemplates 加载全部页面模板（layout + 每页 content 块）。
// 模板来自 embed FS（templates/*.html），编译失败时返回错误。
func NewTemplates() (*Templates, error) {
	layoutData, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, err
	}
	funcs := template.FuncMap{"runRequestKey": uuid.NewString, "formatTime": formatSeconds, "sourceHref": sourceHref, "join": strings.Join, "jsonArray": jsonArray, "json": jsonValue, "questionAction": questionAction, "questionKind": questionKind, "questionStatus": questionStatus, "knowledgeStatus": knowledgeStatus, "updateStatus": knowledgeUpdateStatus, "updateAction": knowledgeUpdateAction, "noteHref": noteHref}

	t := &Templates{pages: map[string]*template.Template{}}

	matches, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		name := filepath.Base(m)
		if name == "layout.html" || name == "note_panel.html" {
			continue
		}
		// 每个页面 = layout + 该页面，组合成一个 template set
		tmpl, err := template.New(name).Funcs(funcs).Parse(string(layoutData))
		if err != nil {
			return nil, fmt.Errorf("解析 layout for %s: %w", name, err)
		}
		panel, err := templateFS.ReadFile("templates/note_panel.html")
		if err != nil {
			return nil, err
		}
		if _, err := tmpl.Parse(string(panel)); err != nil {
			return nil, err
		}
		pageData, err := templateFS.ReadFile(m)
		if err != nil {
			return nil, err
		}
		if _, err := tmpl.Parse(string(pageData)); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", name, err)
		}
		t.pages[name] = tmpl
	}
	return t, nil
}

// jsonArray 把 JSON 数组字符串解析为字符串切片（解析失败返回 nil，模板可 range）。
func jsonArray(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// jsonValue serializes template data for a data-* attribute. html/template
// escapes the attribute boundary and the browser decodes it back to JSON.
func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func sourceHref(sourceType models.SourceType, sourceID string, position float64) string {
	id := url.PathEscape(sourceID)
	if sourceType == models.SourceDocument {
		return "/documents/" + id + "#" + id + "-p" + fmt.Sprintf("%04d", int(position))
	}
	return fmt.Sprintf("/sources/%s/%s?t=%.3f", url.PathEscape(string(sourceType)), id, position)
}

// Render 渲染指定页面：执行 layout 模板，content/title 块由页面文件提供。
func (t *Templates) Render(w io.Writer, name string, data any) error {
	tmpl, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("未知模板: %s", name)
	}
	return tmpl.ExecuteTemplate(w, "layout", data)
}

// formatSeconds 把秒数格式化为 mm:ss 或 h:mm:ss。
func formatSeconds(sec float64) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func knowledgeStatus(status string) string {
	labels := map[string]string{"active": "进行中", "paused": "已暂停", "ended": "已结束", "discover": "寻找选题", "select": "检索与选材", "write": "正在写作", "review": "正在审校", "revise": "正在修订", "review_final": "再次审校", "ready": "已成稿", "written": "已成稿（旧记录）", "needs_review": "需要人工处理", "insufficient": "材料不足", "failed": "处理失败", "complete": "已完成", "succeeded": "执行成功", "queued": "排队中", "pending": "待回答", "answered": "已回答", "later": "稍后回看", "waiting": "等待成稿", "selected": "正在成稿", "duplicate": "方向重复", "admitted": "已纳入外发集合", "skipped": "程序未纳入", "not_read": "尚未读取", "explain": "能解释", "partial": "部分理解", "revisit": "需要重看", "support": "支持", "complement": "补充", "opposition": "反方"}
	if label, ok := labels[status]; ok {
		return label
	}
	return "待处理"
}

func noteHref(n *models.OwnerNote) string {
	var a models.NoteAnchor
	if json.Unmarshal([]byte(n.AnchorJSON), &a) == nil && a.SnapshotID != "" {
		if a.NoPosition {
			return "/evidence/" + url.PathEscape(a.SnapshotID)
		}
		if n.SourceType == "document" {
			return fmt.Sprintf("/evidence/%s?position=%.0f", url.PathEscape(a.SnapshotID), a.Position)
		}
		return fmt.Sprintf("/evidence/%s?t=%.1f", url.PathEscape(a.SnapshotID), a.Position)
	}
	return sourceHref(models.SourceType(n.SourceType), n.SourceID, 0)
}

func questionStatus(status string) string {
	switch status {
	case "active":
		return "进行中"
	case "paused":
		return "暂停"
	case "resolved":
		return "已解决"
	case "archived":
		return "归档"
	}
	return status
}

func questionAction(action string) string {
	switch action {
	case "create":
		return "创建问题"
	case "edit":
		return "修改目标"
	case "status":
		return "确认状态"
	case "link":
		return "确认关联"
	case "suggest":
		return "添加待确认关系"
	case "unlink":
		return "移除关系"
	case "note":
		return "保存个人笔记"
	case "source_purged":
		return "来源清理，移除相关关系"
	}
	return action
}
func questionKind(kind string) string {
	switch kind {
	case "source":
		return "来源"
	case "keypoint":
		return "重点"
	case "note":
		return "笔记"
	case "evidence":
		return "证据"
	case "article":
		return "文章"
	}
	return kind
}
