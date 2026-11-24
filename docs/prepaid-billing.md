# Prepaid billing (to-C mode)

TokenHub ships two deployment modes, configured in **Settings → System settings → Deployment mode**:

- **Internal** (default): the existing postpaid model. Quota policies cap requests, tokens, and daily/monthly cost per API key, user, or project. No balance is enforced.
- **Public to-C**: prepaid enforcement. Every gateway call is admitted only when the paying wallet has a positive balance, and each finished call debits the tenant cost of that request.

## Wallets

Wallets exist at two levels:

- **Personal wallets** pay for API keys with an owner (`owner_user_id`). Keys without an owner bill the **project wallet** of the key's project.
- Wallets are created lazily: the first admin top-up, adjustment, or redeemed code creates the row. In to-C mode a missing wallet reads as a zero balance, so switch the mode only after funding the wallets that must keep working.
- **Exemption** marks a wallet as outside balance enforcement (for internal users or projects inside a public deployment). Exempt wallets still appear in reports, but requests never check or debit them.

Enforcement is admission-time: a wallet at or below zero blocks new `/v1/*` requests with HTTP 402 `insufficient_balance`. Calls already in flight settle normally and may push the balance slightly negative; the next request is then blocked. Balance and amounts are USD, consistent with model pricing, rounded to six decimals. Each request's debit is idempotent per request ID.

## Top-up codes

Administrators generate single-use codes in batches (count, face value, optional expiry, note). The full code is returned exactly once at generation time — like API key secrets, it cannot be shown again. Users redeem codes in **My Wallet**; redemption locks the code row so concurrent attempts cannot double-credit. Unused codes can be revoked.

## Self-registration

In to-C mode the **Allow self-registration** setting opens a public sign-up entry on the login page. Registration requires an email and a password of 8–72 characters; usernames are optional (the email prefix is used otherwise). New accounts get the `user` role and land in the personal console, where they can create their own API keys (the default project is used when they belong to none) and redeem top-up codes. Registration is throttled per client IP.

## Administrator endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET, POST | `/api/admin/wallets` | List wallets; create (and optionally fund) a wallet for a user or project |
| POST | `/api/admin/wallets/{id}/adjust` | Manual credit (positive) or deduction (negative) with a note |
| PATCH | `/api/admin/wallets/{id}` | Set or clear the exemption flag |
| GET | `/api/admin/wallets/{id}/transactions` | Ledger rows for a wallet |
| GET, POST | `/api/admin/topup-codes` | List codes (prefix only); generate a batch (full codes returned once) |
| POST | `/api/admin/topup-codes/{id}/revoke` | Revoke an unused code |

Wallet administration requires the `wallet` resource (platform administrators and team leaders). All wallet and code mutations record administrator audit events.

## Self-service endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/admin/my/wallet` | Caller's balance and latest ledger rows |
| POST | `/api/admin/my/wallet/redeem` | Redeem a top-up code |
| GET | `/api/admin/auth/registration` | Public flag for the login page |
| POST | `/api/admin/auth/register` | Public self-registration (to-C mode + setting enabled) |

## Console pages

- **Cost governance → Prepaid wallets** (admin): wallet list, manual adjustment, exemption toggle, ledger drawer.
- **Cost governance → Top-up codes** (admin): batch generation with one-time display, copy, and export; revocation.
- **My resources → My wallet** (regular users): balance cards, code redemption, personal ledger.

## Notes and limits

- Online payment integration, invoices, CNY pricing, and per-request reserve holds are out of scope for this stage; a small overdraft from in-flight calls is accepted by design.
- The per-IP registration limiter is process-local. Multi-instance deployments should add an edge rate-limiting proxy in front of the register endpoint.
