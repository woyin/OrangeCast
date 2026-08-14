package server

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

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
	funcs := template.FuncMap{"formatTime": formatSeconds, "sourceHref": sourceHref, "join": strings.Join}

	t := &Templates{pages: map[string]*template.Template{}}

	matches, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		name := filepath.Base(m)
		if name == "layout.html" {
			continue
		}
		// 每个页面 = layout + 该页面，组合成一个 template set
		tmpl, err := template.New(name).Funcs(funcs).Parse(string(layoutData))
		if err != nil {
			return nil, fmt.Errorf("解析 layout for %s: %w", name, err)
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
