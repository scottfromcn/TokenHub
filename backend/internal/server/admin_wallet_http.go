package server

import (
	"net/http"
	"strings"
	"time"
)

// Admin and self-service HTTP surface for prepaid wallets and top-up codes.
// Admin routes require the "wallet" resource; personal routes under /my are
// reachable by any authenticated console user.

type adminWalletView struct {
	Wallet
	OwnerName string `json:"owner_name"`
}

func (s *Server) handleAdminWalletsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "wallet", http.MethodGet); !ok {
		return
	}
	ownerKind := strings.TrimSpace(r.URL.Query().Get("owner_kind"))
	wallets, err := s.store.ListWallets(ownerKind)
	if err != nil {
		writeError(w, r, err)
		return
	}
	names := map[string]string{}
	for _, user := range s.store.ListAdminUsers() {
		names[user.ID] = user.Username
	}
	for _, project := range s.store.ListProjects() {
		names[project.ID] = project.Name
	}
	views := make([]adminWalletView, 0, len(wallets))
	for _, wallet := range wallets {
		views = append(views, adminWalletView{Wallet: wallet, OwnerName: names[wallet.OwnerID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": views})
}

func (s *Server) handleAdminWalletsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "wallet", http.MethodPost)
	if !ok {
		return
	}
	var req struct {
		OwnerKind string   `json:"owner_kind"`
		OwnerID   string   `json:"owner_id"`
		AmountUSD *float64 `json:"amount_usd"`
		Note      string   `json:"note"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	wallet, err := s.store.EnsureWallet(strings.TrimSpace(req.OwnerKind), strings.TrimSpace(req.OwnerID))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if req.AmountUSD != nil && *req.AmountUSD != 0 {
		amount := *req.AmountUSD
		if amount > 100000 || amount < -100000 {
			writeError(w, r, NewHTTPError(400, "invalid_request", "amount_usd must be between -100000 and 100000"))
			return
		}
		if wallet, err = s.store.AdjustWallet(wallet.ID, amount, strings.TrimSpace(req.Note), user.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	s.recordAdminAudit(r, user, "create", "wallet", wallet.ID, nil, wallet)
	writeJSON(w, http.StatusCreated, map[string]any{"data": wallet})
}

func (s *Server) handleAdminWalletAdjustPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "wallet", http.MethodPost)
	if !ok {
		return
	}
	walletID := r.PathValue("wallet_id")
	var req struct {
		AmountUSD *float64 `json:"amount_usd"`
		Note      string   `json:"note"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.AmountUSD == nil || *req.AmountUSD == 0 {
		writeError(w, r, NewHTTPError(400, "invalid_request", "amount_usd must be a non-zero amount"))
		return
	}
	amount := *req.AmountUSD
	if amount > 100000 || amount < -100000 {
		writeError(w, r, NewHTTPError(400, "invalid_request", "amount_usd must be between -100000 and 100000"))
		return
	}
	before, _ := s.store.GetWallet(walletID)
	wallet, err := s.store.AdjustWallet(walletID, amount, strings.TrimSpace(req.Note), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "adjust", "wallet", walletID, before, wallet)
	writeJSON(w, http.StatusOK, map[string]any{"data": wallet})
}

func (s *Server) handleAdminWalletPatch(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "wallet", http.MethodPatch)
	if !ok {
		return
	}
	walletID := r.PathValue("wallet_id")
	var req struct {
		Exempt *bool `json:"exempt"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Exempt == nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", "exempt is required"))
		return
	}
	before, _ := s.store.GetWallet(walletID)
	wallet, err := s.store.SetWalletExempt(walletID, *req.Exempt)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "wallet", walletID, before, wallet)
	writeJSON(w, http.StatusOK, map[string]any{"data": wallet})
}

func (s *Server) handleAdminWalletTransactionsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "wallet", http.MethodGet); !ok {
		return
	}
	transactions, err := s.store.ListWalletTransactions(r.PathValue("wallet_id"), queryLimit(r, 100))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": transactions})
}

func (s *Server) handleAdminTopupCodesPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "wallet", http.MethodPost)
	if !ok {
		return
	}
	var req struct {
		Count        *int       `json:"count"`
		FaceValueUSD *float64   `json:"face_value_usd"`
		ExpiresAt    *time.Time `json:"expires_at"`
		Note         string     `json:"note"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	count := 1
	if req.Count != nil {
		count = *req.Count
	}
	faceValue := 0.0
	if req.FaceValueUSD != nil {
		faceValue = *req.FaceValueUSD
	}
	codes, rawCodes, err := s.store.CreateTopupCodes(count, faceValue, req.ExpiresAt, strings.TrimSpace(req.Note), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "topup_code_batch", codes[0].BatchID, nil, map[string]any{
		"count":          len(codes),
		"face_value_usd": faceValue,
		"note":           strings.TrimSpace(req.Note),
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"batch_id": codes[0].BatchID,
		"count":    len(codes),
		// Full codes are returned exactly once, mirroring API key secrets.
		"codes": rawCodes,
	})
}

func (s *Server) handleAdminTopupCodesGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "wallet", http.MethodGet); !ok {
		return
	}
	codes, err := s.store.ListTopupCodes(
		strings.TrimSpace(r.URL.Query().Get("batch_id")),
		strings.TrimSpace(r.URL.Query().Get("status")),
		queryLimit(r, 200),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": codes})
}

func (s *Server) handleAdminTopupCodeRevokePost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "wallet", http.MethodPost)
	if !ok {
		return
	}
	codeID := r.PathValue("code_id")
	if err := s.store.RevokeTopupCode(codeID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "revoke", "topup_code", codeID, nil, nil)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": codeID, "status": "revoked"}})
}

func (s *Server) handleMyWalletGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "my_wallet", http.MethodGet)
	if !ok {
		return
	}
	wallet, transactions, err := s.store.MyWallet(user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":       wallet,
		"transactions": transactions,
	})
}

func (s *Server) handleMyWalletRedeemPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "topup_redeem", http.MethodPost)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	wallet, code, err := s.store.RedeemTopupCode(user.ID, req.Code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":         wallet,
		"face_value_usd": code.FaceValueUSD,
	})
}

func queryLimit(r *http.Request, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return fallback
	}
	parsed := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return fallback
		}
		parsed = parsed*10 + int(char-'0')
		if parsed > 1000 {
			return 1000
		}
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}
