// Prepaid-wallet DTOs mirroring the backend's /api/admin/wallets contract.
// Kept out of types.tsx to respect the source-line ceiling.

export type Wallet = {
  id: string;
  owner_kind: string;
  owner_id: string;
  balance_usd: number;
  exempt: boolean;
  owner_name?: string;
  updated_at?: string;
};

export type WalletTransaction = {
  id: string;
  owner_kind: string;
  owner_id: string;
  type: string;
  amount_usd: number;
  balance_after_usd: number;
  request_id?: string;
  ref_id?: string;
  note?: string;
  created_by?: string;
  created_at: string;
};

export type TopupCode = {
  id: string;
  batch_id: string;
  code_prefix: string;
  face_value_usd: number;
  status: string;
  used_by_user_id?: string;
  used_at?: string;
  expires_at?: string;
  note?: string;
  created_by?: string;
  created_at: string;
};

export type MyWalletResponse = {
  wallet: Wallet;
  transactions: WalletTransaction[];
};
