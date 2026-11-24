package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Prepaid billing: per-user and per-project wallets, a money ledger, and
// single-use top-up codes. Enforcement is admission-time (a non-exempt wallet
// at or below zero blocks new gateway calls in to_c mode) and settlement-time
// (each finished call debits the tenant cost of that request, idempotently per
// request ID).

const (
	walletOwnerUser    = "user"
	walletOwnerProject = "project"

	walletTxnTopupCode  = "topup_code"
	walletTxnTopupAdmin = "topup_admin"
	walletTxnAdjust     = "adjust"
	walletTxnDebit      = "debit"

	topupCodeStatusUnused  = "unused"
	topupCodeStatusUsed    = "used"
	topupCodeStatusRevoked = "revoked"

	topupCodePrefixDefault = "thc_"

	deploymentModeInternal = "internal"
	deploymentModeToC      = "to_c"

	deploymentModeField          = "deployment_mode"
	selfRegistrationEnabledField = "self_registration_enabled"
	walletLowBalanceUSDField     = "wallet_low_balance_usd"

	// Money is stored as float64 USD to match UsageRecord.CostUSD accounting;
	// writes are rounded to micro-dollars so repeated arithmetic stays stable.
	walletMicroUSD = 1e6
)

type Wallet struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	OwnerKind  string    `json:"owner_kind" gorm:"uniqueIndex:idx_wallet_owner,priority:1"`
	OwnerID    string    `json:"owner_id" gorm:"uniqueIndex:idx_wallet_owner,priority:2;index"`
	BalanceUSD float64   `json:"balance_usd"`
	Exempt     bool      `json:"exempt"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type WalletTransaction struct {
	ID              string    `json:"id" gorm:"primaryKey"`
	WalletID        string    `json:"wallet_id" gorm:"index"`
	OwnerKind       string    `json:"owner_kind" gorm:"index"`
	OwnerID         string    `json:"owner_id" gorm:"index"`
	Type            string    `json:"type" gorm:"index"`
	AmountUSD       float64   `json:"amount_usd"`
	BalanceAfterUSD float64   `json:"balance_after_usd"`
	RequestID       *string   `json:"request_id,omitempty" gorm:"uniqueIndex"`
	RefID           string    `json:"ref_id,omitempty"`
	Note            string    `json:"note,omitempty"`
	CreatedBy       string    `json:"created_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type TopupCode struct {
	ID           string     `json:"id" gorm:"primaryKey"`
	BatchID      string     `json:"batch_id" gorm:"index"`
	CodeHash     string     `json:"-" gorm:"uniqueIndex"`
	CodePrefix   string     `json:"code_prefix"`
	FaceValueUSD float64    `json:"face_value_usd"`
	Status       string     `json:"status" gorm:"index"`
	UsedByUserID string     `json:"used_by_user_id,omitempty" gorm:"index"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty" gorm:"index"`
	Note         string     `json:"note,omitempty"`
	CreatedBy    string     `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type deploymentSettings struct {
	Mode              string
	SelfRegistration  bool
	LowBalanceWarnUSD float64
}

func roundWalletAmount(value float64) float64 {
	return math.Round(value*walletMicroUSD) / walletMicroUSD
}

// readDeploymentSettingsTx reads the deployment mode snapshot inside a
// transaction so a mode flip applies to the very next request. Missing fields
// keep their internal-mode defaults, so an unseeded database never enforces
// prepaid billing by accident.
func readDeploymentSettingsTx(tx *gorm.DB) (deploymentSettings, error) {
	settings := deploymentSettings{
		Mode:              deploymentModeInternal,
		SelfRegistration:  false,
		LowBalanceWarnUSD: 1,
	}
	var record AdminResource
	err := tx.Where("id = ? AND kind = ?", gatewaySettingsID, "settings").First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return settings, nil
		}
		return settings, err
	}
	if record.Status != StatusActive {
		return settings, nil
	}
	if mode, ok := stringFieldOK(record.Fields, deploymentModeField); ok {
		switch strings.TrimSpace(mode) {
		case deploymentModeToC:
			settings.Mode = deploymentModeToC
		default:
			settings.Mode = deploymentModeInternal
		}
	}
	if value, ok := boolFieldOK(record.Fields, selfRegistrationEnabledField); ok {
		settings.SelfRegistration = value
	}
	if value, ok := floatFieldOK(record.Fields, walletLowBalanceUSDField); ok && value >= 0 {
		settings.LowBalanceWarnUSD = value
	}
	return settings, nil
}

func stringFieldOK(fields map[string]any, key string) (string, bool) {
	value, ok := fields[key]
	if !ok || value == nil {
		return "", false
	}
	if typed, ok := value.(string); ok {
		return typed, true
	}
	return fmt.Sprintf("%v", value), true
}

func boolFieldOK(fields map[string]any, key string) (bool, bool) {
	value, ok := fields[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true"), true
	default:
		return false, false
	}
}

func floatFieldOK(fields map[string]any, key string) (float64, bool) {
	value, ok := fields[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%g", &parsed); err == nil {
			return parsed, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// walletOwnerForKey resolves which wallet pays for calls made with a key:
// personal wallets for owned keys, project wallets for shared keys.
func walletOwnerForKey(key APIKey, project Project) (string, string) {
	if strings.TrimSpace(key.OwnerUserID) != "" {
		return walletOwnerUser, key.OwnerUserID
	}
	return walletOwnerProject, project.ID
}

func walletScopeID(ownerKind string, ownerID string) string {
	return ownerKind + ":" + ownerID
}

// walletForUpdateTx returns the wallet for an owner inside the caller's
// transaction, locking the wallet scope so concurrent admissions and debits
// serialize. A missing wallet reads as an empty enforced wallet.
func (s *GormStore) walletForUpdateTx(tx *gorm.DB, ownerKind string, ownerID string) (Wallet, bool, error) {
	if err := s.lockScopeForUpdate(tx, "wallet", walletScopeID(ownerKind, ownerID)); err != nil {
		return Wallet{}, false, err
	}
	var wallet Wallet
	err := tx.Where("owner_kind = ? AND owner_id = ?", ownerKind, ownerID).First(&wallet).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Wallet{}, false, nil
		}
		return Wallet{}, false, err
	}
	return wallet, true, nil
}

// admitPrepaidWallet blocks new calls when prepaid billing is active and the
// paying wallet has no balance left. In-flight calls may still settle slightly
// below zero; this check stops the next call.
func (s *GormStore) admitPrepaidWallet(tx *gorm.DB, key APIKey, project Project) error {
	settings, err := readDeploymentSettingsTx(tx)
	if err != nil {
		return err
	}
	if settings.Mode != deploymentModeToC {
		return nil
	}
	ownerKind, ownerID := walletOwnerForKey(key, project)
	wallet, exists, err := s.walletForUpdateTx(tx, ownerKind, ownerID)
	if err != nil {
		return err
	}
	if exists && wallet.Exempt {
		return nil
	}
	if exists && wallet.BalanceUSD > 0 {
		return nil
	}
	scope := "api_key"
	if ownerKind == walletOwnerUser {
		scope = "user"
	}
	blocked := scopedHTTPError(ErrInsufficientBalance, scope)
	if exists {
		blocked.Message = fmt.Sprintf("%s (balance %.6f USD)", blocked.Message, wallet.BalanceUSD)
	}
	return blocked
}

// settlePrepaidWalletDebit records the money movement for a finished call.
// The request_settlement lock plus the RequestLog existence check already make
// finishCallTransaction idempotent; the unique request_id index on debit rows
// is the second line of defense.
func (s *GormStore) settlePrepaidWalletDebit(tx *gorm.DB, key APIKey, project Project, requestID string, costUSD float64, now time.Time) error {
	costUSD = roundWalletAmount(costUSD)
	if costUSD <= 0 {
		return nil
	}
	settings, err := readDeploymentSettingsTx(tx)
	if err != nil {
		return err
	}
	if settings.Mode != deploymentModeToC {
		return nil
	}
	ownerKind, ownerID := walletOwnerForKey(key, project)
	wallet, exists, err := s.walletForUpdateTx(tx, ownerKind, ownerID)
	if err != nil {
		return err
	}
	if !exists || wallet.Exempt {
		return nil
	}
	balanceAfter := roundWalletAmount(wallet.BalanceUSD - costUSD)
	txn := WalletTransaction{
		ID:              NewID("wtx"),
		WalletID:        wallet.ID,
		OwnerKind:       ownerKind,
		OwnerID:         ownerID,
		Type:            walletTxnDebit,
		AmountUSD:       -costUSD,
		BalanceAfterUSD: balanceAfter,
		RefID:           requestID,
	}
	if requestID != "" {
		txn.RequestID = &requestID
	}
	if err := tx.Create(&txn).Error; err != nil {
		return err
	}
	wallet.BalanceUSD = balanceAfter
	return tx.Model(&Wallet{}).Where("id = ?", wallet.ID).Updates(map[string]any{
		"balance_usd": balanceAfter,
		"updated_at":  now,
	}).Error
}

func hashTopupCode(rawCode string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(rawCode)))
	return hex.EncodeToString(digest[:])
}

func topupCodeDisplayPrefix(rawCode string) string {
	trimmed := strings.TrimSpace(rawCode)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

// ensureUserWalletTx creates the personal wallet for a user on first use.
func (s *GormStore) ensureUserWalletTx(tx *gorm.DB, userID string, now time.Time) (Wallet, error) {
	wallet, exists, err := s.walletForUpdateTx(tx, walletOwnerUser, userID)
	if err != nil {
		return Wallet{}, err
	}
	if exists {
		return wallet, nil
	}
	wallet = Wallet{
		ID:        NewID("wal"),
		OwnerKind: walletOwnerUser,
		OwnerID:   userID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := tx.Create(&wallet).Error; err != nil {
		return Wallet{}, err
	}
	return wallet, nil
}

// creditWalletTx applies a signed amount and appends a ledger row.
func creditWalletTx(tx *gorm.DB, wallet Wallet, txnType string, amountUSD float64, requestID *string, refID string, note string, createdBy string, now time.Time) (Wallet, error) {
	amountUSD = roundWalletAmount(amountUSD)
	if amountUSD == 0 {
		return wallet, nil
	}
	balanceAfter := roundWalletAmount(wallet.BalanceUSD + amountUSD)
	txn := WalletTransaction{
		ID:              NewID("wtx"),
		WalletID:        wallet.ID,
		OwnerKind:       wallet.OwnerKind,
		OwnerID:         wallet.OwnerID,
		Type:            txnType,
		AmountUSD:       amountUSD,
		BalanceAfterUSD: balanceAfter,
		RequestID:       requestID,
		RefID:           refID,
		Note:            note,
		CreatedBy:       createdBy,
		CreatedAt:       now,
	}
	if err := tx.Create(&txn).Error; err != nil {
		return Wallet{}, err
	}
	if err := tx.Model(&Wallet{}).Where("id = ?", wallet.ID).Updates(map[string]any{
		"balance_usd": balanceAfter,
		"updated_at":  now,
	}).Error; err != nil {
		return Wallet{}, err
	}
	wallet.BalanceUSD = balanceAfter
	wallet.UpdatedAt = now
	return wallet, nil
}
