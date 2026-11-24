package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupWalletTest(t *testing.T, mode string) (*GormStore, Project, APIKey) {
	t.Helper()
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID: gatewaySettingsID, Kind: "settings", Name: "Gateway", Status: StatusActive,
		Fields: map[string]any{deploymentModeField: mode},
	})
	user, err := store.CreateAdminUser(AdminUser{Username: "wallet-user", Email: "wallet-user@example.test", Role: "user", Status: StatusActive}, "WalletUserPass123!")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Wallet project", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "wallet-key", OwnerUserID: user.ID, Status: StatusActive}, "thk_wallet_owner_key")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name: "wallet-model", Modality: "chat", Status: StatusActive,
		InputPriceUSDPer1M: 1, OutputPriceUSDPer1M: 2,
	})
	t.Cleanup(func() { _ = store.Close() })
	return store, project, key
}

func TestPrepaidWalletBlocksAdmissionWithoutBalance(t *testing.T) {
	store, project, key := setupWalletTest(t, deploymentModeToC)
	if _, err := store.StartCall(context.Background(), project, key, "wallet-model", 0); err == nil || AsHTTPError(err).Status != http.StatusPaymentRequired {
		t.Fatalf("missing wallet in to_c mode should block with 402, got %v", err)
	}
	wallet, err := store.ensureUserWalletTx(store.db, key.OwnerUserID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "wallet-model", 0); err == nil || AsHTTPError(err).Code != "insufficient_balance" {
		t.Fatalf("zero-balance wallet should block with insufficient_balance, got %v", err)
	}
	if _, err := store.AdjustWallet(wallet.ID, 1.5, "seed top-up", "adm_test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "wallet-model", 0); err != nil {
		t.Fatalf("funded wallet should admit: %v", err)
	}
}

func TestPrepaidWalletInternalModeNeverBlocks(t *testing.T) {
	store, project, key := setupWalletTest(t, deploymentModeInternal)
	if _, err := store.StartCall(context.Background(), project, key, "wallet-model", 0); err != nil {
		t.Fatalf("internal mode must not enforce balances: %v", err)
	}
}

func TestPrepaidWalletExemptSkipsEnforcement(t *testing.T) {
	store, project, key := setupWalletTest(t, deploymentModeToC)
	wallet, err := store.ensureUserWalletTx(store.db, key.OwnerUserID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetWalletExempt(wallet.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "wallet-model", 0); err != nil {
		t.Fatalf("exempt wallet should admit without balance: %v", err)
	}
}

func TestPrepaidWalletSettlementDebitsAndIsIdempotent(t *testing.T) {
	store, project, key := setupWalletTest(t, deploymentModeToC)
	wallet, err := store.ensureUserWalletTx(store.db, key.OwnerUserID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdjustWallet(wallet.ID, 5, "top-up", "adm_test"); err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "wallet-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{PromptTokens: 1000, CompletionTokens: 1000}, http.StatusOK, "", "127.0.0.1", "wallet-test")
	// Replaying settlement must not debit twice.
	store.FinishCall(call, RouteSelection{}, Usage{PromptTokens: 1000, CompletionTokens: 1000}, http.StatusOK, "", "127.0.0.1", "wallet-test")

	after, err := store.GetWallet(wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 1000 input tokens at 1 USD/1M = 0.001; 1000 output at 2 USD/1M = 0.002.
	if diff := after.BalanceUSD - 4.997; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after debit = %.6f, want 4.997", after.BalanceUSD)
	}
	_, transactions, err := store.MyWallet(key.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 2 {
		t.Fatalf("ledger rows = %d (top-up + one debit), want 2", len(transactions))
	}
	if transactions[0].Type != walletTxnDebit || transactions[0].AmountUSD != -0.003 {
		t.Fatalf("unexpected debit row: %+v", transactions[0])
	}
}

func TestPrepaidWalletSharedKeySettlesProjectWallet(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	store.CreateResource("settings", AdminResource{
		ID: gatewaySettingsID, Kind: "settings", Name: "Gateway", Status: StatusActive,
		Fields: map[string]any{deploymentModeField: deploymentModeToC},
	})
	project := store.CreateProject(Project{Name: "Shared", Status: StatusActive})
	sharedKey, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "shared", Status: StatusActive}, "thk_wallet_shared_key")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "wallet-model", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 1})
	if _, err := store.StartCall(context.Background(), project, sharedKey, "wallet-model", 0); err == nil || AsHTTPError(err).Code != "insufficient_balance" {
		t.Fatalf("unfunded project wallet should block, got %v", err)
	}
	projectWallet, err := store.EnsureWallet(walletOwnerProject, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdjustWallet(projectWallet.ID, 1, "project top-up", "adm_test"); err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, sharedKey, "wallet-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{PromptTokens: 200000}, http.StatusOK, "", "127.0.0.1", "wallet-test")
	funded, err := store.GetWallet(projectWallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := funded.BalanceUSD - 0.8; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("project wallet balance = %.6f, want 0.8", funded.BalanceUSD)
	}
}

func TestTopupCodeRedeemIsSingleUse(t *testing.T) {
	store, _, key := setupWalletTest(t, deploymentModeToC)
	codes, raw, err := store.CreateTopupCodes(3, 2.5, nil, "batch note", "adm_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 3 || codes[0].Status != topupCodeStatusUnused || !strings.HasPrefix(raw[0], topupCodePrefixDefault) {
		t.Fatalf("unexpected generated codes: %+v %v", codes[0], raw[0])
	}
	wallet, _, err := store.RedeemTopupCode(key.OwnerUserID, raw[1])
	if err != nil {
		t.Fatal(err)
	}
	if diff := wallet.BalanceUSD - 2.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after redeem = %.6f, want 2.5", wallet.BalanceUSD)
	}
	if _, _, err := store.RedeemTopupCode(key.OwnerUserID, raw[1]); err == nil || AsHTTPError(err).Code != "topup_code_already_used" {
		t.Fatalf("second redeem should fail with already_used, got %v", err)
	}
	if _, _, err := store.RedeemTopupCode(key.OwnerUserID, "thc_does_not_exist"); err == nil || AsHTTPError(err).Code != "topup_code_not_found" {
		t.Fatalf("unknown code should fail with not_found, got %v", err)
	}
	if err := store.RevokeTopupCode(codes[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RedeemTopupCode(key.OwnerUserID, raw[2]); err == nil || AsHTTPError(err).Code != "topup_code_revoked" {
		t.Fatalf("revoked code should fail with revoked, got %v", err)
	}
	if err := store.RevokeTopupCode(codes[1].ID); err == nil || AsHTTPError(err).Code != "topup_code_not_revocable" {
		t.Fatalf("used code must not be revocable, got %v", err)
	}
}

func TestTopupCodeRedeemConcurrentSingleUse(t *testing.T) {
	store, _, key := setupWalletTest(t, deploymentModeToC)
	_, raw, err := store.CreateTopupCodes(1, 10, nil, "", "adm_test")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.RedeemTopupCode(key.OwnerUserID, raw[0])
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent redeems succeeded %d times, want exactly 1", successes)
	}
	wallet, _, err := store.MyWallet(key.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := wallet.BalanceUSD - 10; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after concurrent redeem = %.6f, want 10", wallet.BalanceUSD)
	}
}

func TestTopupCodeExpiry(t *testing.T) {
	store, _, key := setupWalletTest(t, deploymentModeToC)
	expired := time.Now().UTC().Add(-time.Hour)
	_, raw, err := store.CreateTopupCodes(1, 1, &expired, "", "adm_test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RedeemTopupCode(key.OwnerUserID, raw[0]); err == nil || AsHTTPError(err).Code != "topup_code_expired" {
		t.Fatalf("expired code should fail with expired, got %v", err)
	}
}

func TestSelfRegistrationGateAndFlow(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	store.CreateResource("settings", AdminResource{
		ID: gatewaySettingsID, Kind: "settings", Name: "Gateway", Status: StatusActive,
		Fields: map[string]any{deploymentModeField: deploymentModeInternal, selfRegistrationEnabledField: true},
	})
	handler := New(store).Handler()
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/register", map[string]string{"email": "someone@example.test", "password": "Passw0rd!123"}, ""); resp.Code != http.StatusForbidden {
		t.Fatalf("internal mode registration status = %d, want 403", resp.Code)
	}
	if _, err := store.UpdateResource("settings", gatewaySettingsID, AdminResource{
		ID: gatewaySettingsID, Kind: "settings", Status: StatusActive,
		Fields: map[string]any{deploymentModeField: deploymentModeToC, selfRegistrationEnabledField: true},
	}); err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/register", map[string]string{"email": "someone@example.test", "password": "Passw0rd!123"}, ""); resp.Code != http.StatusCreated {
		t.Fatalf("to_c registration status = %d, want 201: %v", resp.Code, resp.Body)
	}
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/register", map[string]string{"email": "someone@example.test", "password": "Passw0rd!123"}, ""); resp.Code != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d, want 409", resp.Code)
	}
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/register", map[string]string{"email": "not-an-email", "password": "Passw0rd!123"}, ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid email status = %d, want 400", resp.Code)
	}
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/register", map[string]string{"email": "short@example.test", "password": "short"}, ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", resp.Code)
	}
	status := doJSON(t, handler, http.MethodGet, "/api/admin/auth/registration", nil, "")
	if !strings.Contains(status.Body, `"registration_open":true`) {
		t.Fatalf("registration status should be open: %s", status.Body)
	}
}

func TestWalletEndpointsAuthorization(t *testing.T) {
	store, _, key := setupWalletTest(t, deploymentModeToC)
	wallet, err := store.ensureUserWalletTx(store.db, key.OwnerUserID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store).Handler()
	// Ordinary users can read their own wallet but cannot adjust any wallet.
	userToken := loginForTest(t, handler, "wallet-user", "WalletUserPass123!")
	if resp := doJSON(t, handler, http.MethodGet, "/api/admin/my/wallet", nil, userToken); resp.Code != http.StatusOK {
		t.Fatalf("my wallet status = %d, want 200", resp.Code)
	}
	if resp := doJSON(t, handler, http.MethodPost, "/api/admin/wallets/"+wallet.ID+"/adjust", map[string]any{"amount_usd": 100}, userToken); resp.Code != http.StatusForbidden {
		t.Fatalf("user adjust status = %d, want 403", resp.Code)
	}
	if resp := doJSON(t, handler, http.MethodGet, "/api/admin/wallets", nil, userToken); resp.Code != http.StatusForbidden {
		t.Fatalf("user wallet list status = %d, want 403", resp.Code)
	}
}

func loginForTest(t *testing.T, handler http.Handler, identity string, password string) string {
	t.Helper()
	resp := doJSON(t, handler, http.MethodPost, "/api/admin/auth/login", map[string]string{"identity": identity, "password": password}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("login status = %d: %v", resp.Code, resp.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Token
}
