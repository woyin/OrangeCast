// creation_review_workflow.go 独立主张审校与 durable 风格审校的 HTTP 入口（R20）。
// HTTP 只持久化意图：claims POST 入队（重复点击复用、failed 重试同 job）；
// 新契约文章的 EvidenceReview 在 Provider 之前 409（EvidenceReview 不能替代
// ClaimReview）；新契约 style POST 只入队 durable 任务，绝不同步调用 Provider。
package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/store"
)

// handleClaimReviewRun POST /workbench/reviews/claims（R20）：
// 对精确 revision 入队独立主张审校持久任务。重复点击（queued/running/succeeded）
// 复用同一 job；failed 再点真实重试同一 job（reset queued）。重定向 draft 页面。
func (srv *Server) handleClaimReviewRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revisionID := strings.TrimSpace(r.FormValue("revision_id"))
	if revisionID == "" {
		http.Error(w, "缺少 revision_id", http.StatusBadRequest)
		return
	}
	job, err := srv.store.EnqueueRevisionReview(r.Context(), revisionID, store.ReviewKindClaim)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "文章修订不存在", http.StatusNotFound)
			return
		}
		writeEditorialError(w, err)
		return
	}
	// job.SourceID 即 draft 身份；重复点击/重试都重定向同一页面。
	http.Redirect(w, r, "/workbench/drafts/"+job.SourceID, http.StatusSeeOther)
}
