package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func TestLearningExportHTTPInvalidBodiesAndItemMethods(t *testing.T) {
	srv := newTestServer(t)
	for _, handler := range []http.HandlerFunc{srv.handleLearningExportPreview, srv.handleLearningExportCreate} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/learning-exports/preview", strings.NewReader("selection="+strings.Repeat("x", 17000)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		handler(rec, r)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "表单无效") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"POST", "/learning-exports/any/status", 405}, {"GET", "/learning-exports//status", 404}, {"GET", "/learning-exports/one", 404}, {"GET", "/learning-exports/one/status/extra", 404}} {
		rec := httptest.NewRecorder()
		srv.handleLearningExportItem(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatal(tc, rec.Code)
		}
	}
}

func TestLearningExportHTTPStorageFailureDoesNotLeakMaterial(t *testing.T) {
	for _, table := range []string{"learning_exports", "learning_questions", "editorial_profiles", "themes"} {
		t.Run(table, func(t *testing.T) {
			srv := newTestServer(t)
			if _, e := srv.store.EnsureDefaultEditorialProfile(t.Context()); e != nil {
				t.Fatal(e)
			}
			if _, e := srv.store.DB.Exec("ALTER TABLE " + table + " RENAME TO unavailable_export_test"); e != nil {
				t.Fatal(e)
			}
			rec := httptest.NewRecorder()
			srv.handleLearningExports(rec, httptest.NewRequest("GET", "/learning-exports", nil))
			if rec.Code != 500 || strings.Contains(rec.Body.String(), "unavailable_export_test") || !strings.Contains(rec.Body.String(), "请稍后重试") {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLearningExportHTTPOlderQuestionAndDeletedScopeLabels(t *testing.T) {
	srv := newTestServer(t)
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "历史范围仍保留可读标题"})
	if e != nil {
		t.Fatal(e)
	}
	// The first result lies outside the bounded 100-item picker, but the selected
	// exact ID must still resolve without asking an Owner to enter a UUID.
	if _, e = srv.store.DB.Exec(`UPDATE learning_questions SET updated_at='2000-01-01' WHERE id=?`, q.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.DB.Exec(`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<101) INSERT INTO learning_questions(id,body) SELECT 'newer-export-'||i,'新问题 '||i FROM n`); e != nil {
		t.Fatal(e)
	}
	rec := httptest.NewRecorder()
	srv.handleLearningExports(rec, httptest.NewRequest("GET", "/learning-exports?kind=question&id="+q.ID, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "历史范围仍保留可读标题") || !strings.Contains(rec.Body.String(), `value="question:`+q.ID+`" selected`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	p, e := srv.store.PreviewLearningExport(t.Context(), store.LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner"); e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.DB.Exec("DELETE FROM learning_questions WHERE id=?", q.ID); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"question", "theme"} {
		rec = httptest.NewRecorder()
		srv.handleLearningExports(rec, httptest.NewRequest("GET", "/learning-exports?kind="+kind+"&id=removed", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "材料已删除，请重新选择范围") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
}

func TestLearningExportHTTPReadyRevalidationAndDownloadConfinement(t *testing.T) {
	for _, scenario := range []string{"stale", "corrupt-snapshot", "outside-root"} {
		t.Run(scenario, func(t *testing.T) {
			srv := newTestServer(t)
			q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "私密范围正文"})
			if e != nil {
				t.Fatal(e)
			}
			p, e := srv.store.PreviewLearningExport(t.Context(), store.LearningExportScope{Kind: "question", ID: q.ID})
			if e != nil {
				t.Fatal(e)
			}
			v, e := srv.store.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner")
			if e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(srv.cfg.DataDir, "learning-exports")
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if scenario == "outside-root" {
				dir = t.TempDir()
			}
			path := filepath.Join(dir, v.ID+".zip")
			if e = os.WriteFile(path, []byte("private ZIP bytes"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = srv.store.PublishLearningExport(t.Context(), v.ID, path); e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "stale":
				_, e = srv.store.DB.Exec(`UPDATE learning_questions SET body='已撤回旧正文' WHERE id=?`, q.ID)
			case "corrupt-snapshot":
				_, e = srv.store.DB.Exec(`UPDATE learning_export_previews SET snapshot_json='{' WHERE id=?`, p.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			rec := httptest.NewRecorder()
			srv.handleLearningExportItem(rec, httptest.NewRequest("GET", "/learning-exports/"+v.ID+"/status", nil))
			want := 200
			if scenario == "corrupt-snapshot" {
				want = 500
			}
			if rec.Code != want || strings.Contains(rec.Body.String(), "private ZIP bytes") || strings.Contains(rec.Body.String(), path) {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if scenario == "stale" && !strings.Contains(rec.Body.String(), `"status":"expired"`) {
				t.Fatal(rec.Body.String())
			}
			rec = httptest.NewRecorder()
			srv.handleLearningExportItem(rec, httptest.NewRequest("GET", "/learning-exports/"+v.ID+"/download", nil))
			if scenario == "outside-root" && rec.Code != 404 {
				t.Fatal("escaped download root", rec.Code, rec.Body.String())
			}
			if scenario == "stale" && rec.Code != 409 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if scenario == "corrupt-snapshot" && rec.Code != 500 {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLearningExportHTTPPreviewMissingSelection(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/learning-exports/preview", strings.NewReader(url.Values{"selection": {"question:missing"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.handleLearningExportPreview(rec, req)
	if rec.Code != 404 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
