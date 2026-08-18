package server

import (
	"context"
	"net/http"

	"github.com/woyin/orangecast/internal/provider"
)

// scheduleProposalRefill keeps the proposed pool topped up without making a
// paid Provider call during a normal GET /workbench request. A single-owner
// instance only needs one in-process refill at a time for each profile.
func (srv *Server) scheduleProposalRefill(profileID string) {
	if !srv.autoRefill || profileID == "" {
		return
	}
	srv.refillMu.Lock()
	if srv.refilling[profileID] {
		srv.refillMu.Unlock()
		return
	}
	srv.refilling[profileID] = true
	srv.refillErrors[profileID] = ""
	srv.refillMu.Unlock()
	go func() {
		defer func() {
			srv.refillMu.Lock()
			delete(srv.refilling, profileID)
			srv.refillMu.Unlock()
		}()
		_, err := srv.refillProposalPool(context.Background(), profileID)
		if err != nil {
			srv.refillMu.Lock()
			srv.refillErrors[profileID] = err.Error()
			srv.refillMu.Unlock()
		}
	}()
}

func (srv *Server) proposalRefillState(profileID string) (bool, string) {
	srv.refillMu.Lock()
	defer srv.refillMu.Unlock()
	return srv.refilling[profileID], srv.refillErrors[profileID]
}

func (srv *Server) refillProposalPool(ctx context.Context, profileID string) (int, error) {
	proposals, err := srv.store.ListArticleProposals(ctx, profileID)
	if err != nil {
		return 0, err
	}
	proposed := 0
	for _, proposal := range proposals {
		if proposal.Status == "proposed" {
			proposed++
		}
	}
	needed := scoutBrainstormCount - proposed
	if needed <= 0 {
		return 0, nil
	}
	return srv.runScoutContext(ctx, profileID, scoutOptions{Mode: provider.ScoutModeCrossEpisode, ProposalCount: needed})
}

// handleProposalRefill is the visible manual fallback for an automatic refill
// that is still running or was blocked by a Provider/budget configuration.
func (srv *Server) handleProposalRefill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := r.FormValue("profile_id")
	srv.scheduleProposalRefill(profileID)
	http.Redirect(w, r, "/workbench?profile="+profileID+"&refill=scheduled", http.StatusSeeOther)
}
