package server

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Store methods behind the prepaid-wallet feature: wallet administration,
// ledger reads, top-up code batches, and self-service redemption.

// DeploymentSettings returns the deployment-mode snapshot for callers outside
// a gateway transaction (login page flags, self-registration).
func (s *GormStore) DeploymentSettings() (deploymentSettings, error) {
	return readDeploymentSettingsTx(s.db)
}

// EnsureWallet returns the wallet for an owner, creating it on first use so
// administrators can fund projects and users that have never been billed.
func (s *GormStore) EnsureWallet(ownerKind string, ownerID string) (Wallet, error) {
	if ownerKind != walletOwnerUser && ownerKind != walletOwnerProject {
		return Wallet{}, NewHTTPError(400, "invalid_wallet_owner", "owner_kind must be user or project")
	}
	if strings.TrimSpace(ownerID) == "" {
		return Wallet{}, NewHTTPError(400, "invalid_wallet_owner", "owner_id is required")
	}
	if ownerKind == walletOwnerProject {
		if _, ok := s.GetProject(ownerID); !ok {
			return Wallet{}, NewHTTPError(404, "project_not_found", "Project not found")
		}
	} else {
		found := false
		for _, user := range s.ListAdminUsers() {
			if user.ID == ownerID {
				found = true
				break
			}
		}
		if !found {
			return Wallet{}, NewHTTPError(404, "admin_user_not_found", "User not found")
		}
	}
	var ensured Wallet
	err := s.db.Transaction(func(tx *gorm.DB) error {
		wallet, exists, err := s.walletForUpdateTx(tx, ownerKind, ownerID)
		if err != nil {
			return err
		}
		if exists {
			ensured = wallet
			return nil
		}
		wallet = Wallet{
			ID:        NewID("wal"),
			OwnerKind: ownerKind,
			OwnerID:   ownerID,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := tx.Create(&wallet).Error; err != nil {
			return err
		}
		ensured = wallet
		return nil
	})
	return ensured, err
}

func (s *GormStore) ListWallets(ownerKind string) ([]Wallet, error) {
	query := s.db.Order("updated_at desc")
	if ownerKind != "" {
		query = query.Where("owner_kind = ?", ownerKind)
	}
	var wallets []Wallet
	if err := query.Find(&wallets).Error; err != nil {
		return nil, err
	}
	return wallets, nil
}

func (s *GormStore) GetWallet(id string) (Wallet, error) {
	var wallet Wallet
	if err := s.db.First(&wallet, "id = ?", id).Error; err != nil {
		return Wallet{}, notFound(err, "wallet_not_found", "Wallet not found")
	}
	return wallet, nil
}

func (s *GormStore) AdjustWallet(id string, amountUSD float64, note string, actor string) (Wallet, error) {
	var adjusted Wallet
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var wallet Wallet
		if err := tx.First(&wallet, "id = ?", id).Error; err != nil {
			return notFound(err, "wallet_not_found", "Wallet not found")
		}
		if err := s.lockScopeForUpdate(tx, "wallet", walletScopeID(wallet.OwnerKind, wallet.OwnerID)); err != nil {
			return err
		}
		txnType := walletTxnAdjust
		if amountUSD > 0 {
			txnType = walletTxnTopupAdmin
		}
		updated, err := creditWalletTx(tx, wallet, txnType, amountUSD, nil, "", note, actor, time.Now().UTC())
		if err != nil {
			return err
		}
		adjusted = updated
		return nil
	})
	return adjusted, err
}

func (s *GormStore) SetWalletExempt(id string, exempt bool) (Wallet, error) {
	var wallet Wallet
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&wallet, "id = ?", id).Error; err != nil {
			return notFound(err, "wallet_not_found", "Wallet not found")
		}
		if err := s.lockScopeForUpdate(tx, "wallet", walletScopeID(wallet.OwnerKind, wallet.OwnerID)); err != nil {
			return err
		}
		wallet.Exempt = exempt
		return tx.Model(&Wallet{}).Where("id = ?", id).Updates(map[string]any{
			"exempt":     exempt,
			"updated_at": time.Now().UTC(),
		}).Error
	})
	if err != nil {
		return Wallet{}, err
	}
	return wallet, nil
}

func (s *GormStore) ListWalletTransactions(walletID string, limit int) ([]WalletTransaction, error) {
	var wallet Wallet
	if err := s.db.First(&wallet, "id = ?", walletID).Error; err != nil {
		return nil, notFound(err, "wallet_not_found", "Wallet not found")
	}
	return s.listWalletTransactions(s.db, wallet.OwnerKind, wallet.OwnerID, limit)
}

func (s *GormStore) listWalletTransactions(query *gorm.DB, ownerKind string, ownerID string, limit int) ([]WalletTransaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var transactions []WalletTransaction
	err := query.Where("owner_kind = ? AND owner_id = ?", ownerKind, ownerID).
		Order("created_at desc").Limit(limit).Find(&transactions).Error
	return transactions, err
}

// MyWallet returns the caller's personal wallet and its latest ledger rows.
// A user without a wallet yet reads as a zero-balance wallet with no rows.
func (s *GormStore) MyWallet(userID string) (Wallet, []WalletTransaction, error) {
	var wallet Wallet
	err := s.db.Where("owner_kind = ? AND owner_id = ?", walletOwnerUser, userID).First(&wallet).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return Wallet{}, nil, err
		}
		wallet = Wallet{OwnerKind: walletOwnerUser, OwnerID: userID}
	}
	transactions, err := s.listWalletTransactions(s.db, walletOwnerUser, userID, 100)
	if err != nil {
		return Wallet{}, nil, err
	}
	return wallet, transactions, nil
}

func (s *GormStore) CreateTopupCodes(count int, faceValueUSD float64, expiresAt *time.Time, note string, actor string) ([]TopupCode, []string, error) {
	if count < 1 || count > 1000 {
		return nil, nil, NewHTTPError(400, "invalid_topup_count", "Top-up code count must be between 1 and 1000")
	}
	if faceValueUSD <= 0 || faceValueUSD > 100000 {
		return nil, nil, NewHTTPError(400, "invalid_topup_face_value", "Top-up face value must be between 0 and 100000 USD")
	}
	batchID := NewID("tcb")
	now := time.Now().UTC()
	codes := make([]TopupCode, 0, count)
	rawCodes := make([]string, 0, count)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < count; i++ {
			raw := GenerateAPIKeyWithOptions(topupCodePrefixDefault, 32)
			code := TopupCode{
				ID:           NewID("tcd"),
				BatchID:      batchID,
				CodeHash:     hashTopupCode(raw),
				CodePrefix:   topupCodeDisplayPrefix(raw),
				FaceValueUSD: roundWalletAmount(faceValueUSD),
				Status:       topupCodeStatusUnused,
				ExpiresAt:    expiresAt,
				Note:         note,
				CreatedBy:    actor,
				CreatedAt:    now,
			}
			if err := tx.Create(&code).Error; err != nil {
				return err
			}
			codes = append(codes, code)
			rawCodes = append(rawCodes, raw)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return codes, rawCodes, nil
}

func (s *GormStore) ListTopupCodes(batchID string, status string, limit int) ([]TopupCode, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := s.db.Order("created_at desc").Limit(limit)
	if batchID != "" {
		query = query.Where("batch_id = ?", batchID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var codes []TopupCode
	if err := query.Find(&codes).Error; err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *GormStore) RevokeTopupCode(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var code TopupCode
		if err := tx.First(&code, "id = ?", id).Error; err != nil {
			return notFound(err, "topup_code_not_found", "Top-up code not found")
		}
		if err := s.lockScopeForUpdate(tx, "topup_code", code.ID); err != nil {
			return err
		}
		if code.Status != topupCodeStatusUnused {
			return NewHTTPError(409, "topup_code_not_revocable", "Only unused top-up codes can be revoked")
		}
		return tx.Model(&TopupCode{}).Where("id = ?", id).Update("status", topupCodeStatusRevoked).Error
	})
}

// RedeemTopupCode applies a single-use code to the caller's personal wallet.
// The code row is locked for the status flip, so concurrent redemptions of the
// same code cannot both succeed.
func (s *GormStore) RedeemTopupCode(userID string, rawCode string) (Wallet, TopupCode, error) {
	if strings.TrimSpace(rawCode) == "" {
		return Wallet{}, TopupCode{}, NewHTTPError(400, "topup_code_invalid", "Top-up code is required")
	}
	now := time.Now().UTC()
	var wallet Wallet
	var redeemed TopupCode
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var code TopupCode
		if err := tx.Where("code_hash = ?", hashTopupCode(rawCode)).First(&code).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return NewHTTPError(404, "topup_code_not_found", "Top-up code not found")
			}
			return err
		}
		if err := s.lockScopeForUpdate(tx, "topup_code", code.ID); err != nil {
			return err
		}
		if code.Status == topupCodeStatusUsed {
			return NewHTTPError(409, "topup_code_already_used", "Top-up code has already been used")
		}
		if code.Status == topupCodeStatusRevoked {
			return NewHTTPError(409, "topup_code_revoked", "Top-up code has been revoked")
		}
		if code.ExpiresAt != nil && now.After(*code.ExpiresAt) {
			return NewHTTPError(410, "topup_code_expired", "Top-up code has expired")
		}
		personal, err := s.ensureUserWalletTx(tx, userID, now)
		if err != nil {
			return err
		}
		updated, err := creditWalletTx(tx, personal, walletTxnTopupCode, code.FaceValueUSD, nil, code.ID, "", "", now)
		if err != nil {
			return err
		}
		code.Status = topupCodeStatusUsed
		code.UsedByUserID = userID
		code.UsedAt = &now
		if err := tx.Model(&TopupCode{}).Where("id = ?", code.ID).Updates(map[string]any{
			"status":          topupCodeStatusUsed,
			"used_by_user_id": userID,
			"used_at":         now,
		}).Error; err != nil {
			return err
		}
		wallet = updated
		redeemed = code
		return nil
	})
	if err != nil {
		return Wallet{}, TopupCode{}, err
	}
	return wallet, redeemed, nil
}
