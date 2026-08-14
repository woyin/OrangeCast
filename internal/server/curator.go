package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/woyin/orangecast/internal/provider"
)

func (srv *Server) handleCuratorRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	result, err := srv.runCurator(r, r.FormValue("proposal_id"))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+result.ProfileID+"#brief-"+result.BriefID, http.StatusSeeOther)
}

func (srv *Server) validateEditorialMaterialsProvider(ctx context.Context, materials []provider.ArticleMaterial, providerName string) error {
	for _, material := range materials {
		keyPoint, err := srv.store.GetKeyPoint(ctx, material.KeyPointID)
		if err != nil {
			return err
		}
		allowed, err := srv.store.CanSendSourceToProvider(ctx, keyPoint.SourceType, keyPoint.SourceID, providerName)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("备用 Provider 不符合素材 ModelDataPolicy")
		}
	}
	return nil
}
