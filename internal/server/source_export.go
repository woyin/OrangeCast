package server

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/woyin/orangecast/internal/notesync"
)

// handleSourceExport 把当前知识卡片同步到外部笔记工具：
// POST /sources/{type}/{id}/export/readwise 推送高光；/obsidian 写入本地 vault 目录。
func (srv *Server) handleSourceExport(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 2 || rest[0] != "export" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	back := func(ok bool, msg string) {
		q := url.Values{"export_msg": {msg}}
		if ok {
			q.Set("export_ok", "1")
		}
		http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID+"?"+q.Encode(), http.StatusSeeOther)
	}
	exp, eerr := srv.buildSourceExport(r.Context(), sourceType, sourceID, r.FormValue("with_generated") == "1")
	if eerr != nil {
		back(false, eerr.Msg)
		return
	}
	switch rest[1] {
	case "readwise":
		if srv.readwise == nil || srv.readwise.Token == "" {
			back(false, "未配置 READWISE_TOKEN")
			return
		}
		hs := notesync.Highlights(&exp.Card, exp.Segments, exp.Title, "", srv.cfg.PublicURL, string(sourceType), sourceID)
		if len(hs) == 0 {
			back(false, "没有可推送的高光")
			return
		}
		n, err := srv.readwise.Push(r.Context(), hs)
		if err != nil {
			back(false, err.Error())
			return
		}
		back(true, "已推送 "+strconv.Itoa(n)+" 条高光到 Readwise")
	case "obsidian":
		if srv.cfg.ObsidianExportDir == "" {
			back(false, "未配置 OBSIDIAN_EXPORT_DIR")
			return
		}
		if _, err := notesync.WriteObsidian(srv.cfg.ObsidianExportDir, exp.Title, exp.Markdown); err != nil {
			back(false, "写入 Obsidian 失败："+err.Error())
			return
		}
		back(true, "已写入 Obsidian：《"+exp.Title+"》")
	default:
		http.NotFound(w, r)
	}
}
