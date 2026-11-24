import { Gift, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { type AdminUser, type ApiContext } from "../core/types";
import { type MyWalletResponse } from "../core/wallet";
import { formatTime } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable } from "../shared/ui";

function walletUSD(value: number) {
  return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", maximumFractionDigits: 6 }).format(value);
}

function walletTxnTypeLabel(type: string) {
  switch (type) {
    case "topup_code":
      return tx("兑换码充值");
    case "topup_admin":
      return tx("管理员充值");
    case "adjust":
      return tx("调账扣减");
    case "debit":
      return tx("请求扣费");
    default:
      return type;
  }
}

export function MyWalletView({ api, user }: { api: ApiContext; user: AdminUser }) {
  const [payload, setPayload] = useState<MyWalletResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [code, setCode] = useState("");
  const [redeemMessage, setRedeemMessage] = useState("");
  const [redeemError, setRedeemError] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    const controller = new AbortController();
    setLoading(true);
    adminFetch(api, "/api/admin/my/wallet", { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("钱包加载失败")));
        return await response.json() as MyWalletResponse;
      })
      .then((payload) => setPayload(payload))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setError(reason instanceof Error ? reason.message : tx("钱包加载失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api]);

  useEffect(() => load(), [load]);

  async function submitRedeem() {
    setRedeemError("");
    setRedeemMessage("");
    if (!code.trim()) {
      setRedeemError(tx("请输入兑换码"));
      return;
    }
    setBusy(true);
    try {
      const response = await adminFetch(api, "/api/admin/my/wallet/redeem", {
        method: "POST",
        body: JSON.stringify({ code: code.trim() }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("兑换失败")));
      const result = await response.json() as { face_value_usd: number };
      setRedeemMessage(formatTranslationTemplate(tx("充值成功，到账 {amount}"), { amount: walletUSD(result.face_value_usd) }));
      setCode("");
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setRedeemError(reason instanceof Error ? reason.message : tx("兑换失败"));
    } finally {
      setBusy(false);
    }
  }

  const wallet = payload?.wallet;
  const transactions = payload?.transactions ?? [];
  const hasBalance = !!wallet && wallet.balance_usd > 0;

  return (
    <div>
      <header className="page-header">
        <div className="page-context-header">
          <div className="page-context-main">
            <p className="eyebrow">{tx("我的钱包")}</p>
            <h1>{tx("我的钱包")}</h1>
          </div>
          <div className="page-context-side">
            <button className="secondary-button" type="button" onClick={load}>
              <RefreshCw size={14} />{tx("刷新")}
            </button>
          </div>
        </div>
        <p className="page-desc">{tx("预充值余额按请求实际用量扣减；余额耗尽后新请求会被拒绝，请及时充值。")}</p>
      </header>

      {error ? <div className="status-line error">{error}</div> : null}

      <div className="metrics">
        <div className="metric">
          <span className="metric-label">{tx("当前余额")}</span>
          <strong className="metric-value" style={{ color: hasBalance ? undefined : "var(--red)" }}>
            {wallet ? walletUSD(wallet.balance_usd) : tx("加载中…")}
          </strong>
        </div>
        <div className="metric">
          <span className="metric-label">{tx("累计充值")}</span>
          <strong className="metric-value">
            {walletUSD(transactions.filter((txn) => txn.amount_usd > 0 && txn.type !== "adjust").reduce((sum, txn) => sum + txn.amount_usd, 0))}
          </strong>
        </div>
        <div className="metric">
          <span className="metric-label">{tx("累计消费")}</span>
          <strong className="metric-value">
            {walletUSD(-transactions.filter((txn) => txn.amount_usd < 0).reduce((sum, txn) => sum + txn.amount_usd, 0))}
          </strong>
        </div>
        <div className="metric">
          <span className="metric-label">{tx("账户")}</span>
          <strong className="metric-value" style={{ fontSize: 16 }}>{user.username || user.email}</strong>
        </div>
      </div>

      <DataSection title={tx("兑换充值码")}>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
          <input
            className="search-box"
            style={{ flex: "1 1 320px", maxWidth: 420 }}
            value={code}
            onChange={(event) => setCode(event.target.value)}
            placeholder={tx("粘贴完整兑换码，例如 thc_…")}
            aria-label={tx("兑换码")}
          />
          <button className="button" type="button" disabled={busy} onClick={submitRedeem}>
            <Gift size={15} />{tx("兑换")}
          </button>
        </div>
        {redeemMessage ? <div className="status-line success" style={{ marginTop: 10 }}>{redeemMessage}</div> : null}
        {redeemError ? <div className="status-line error" style={{ marginTop: 10 }}>{redeemError}</div> : null}
      </DataSection>

      <DataSection title={tx("流水明细")}>
        {loading ? (
          <p className="page-desc">{tx("加载中…")}</p>
        ) : (
          <SimpleTable
            paginationKey="my-wallet-txns"
            columns={[tx("时间"), tx("类型"), tx("金额"), tx("余额"), tx("关联")]}
            rows={transactions.map((txn) => [
              <span key="t">{formatTime(txn.created_at)}</span>,
              <span key="type">{walletTxnTypeLabel(txn.type)}</span>,
              <strong key="amt" style={{ color: txn.amount_usd >= 0 ? "var(--pos)" : undefined }}>{walletUSD(txn.amount_usd)}</strong>,
              <span key="after">{walletUSD(txn.balance_after_usd)}</span>,
              <span key="ref">{txn.ref_id || txn.note || "-"}</span>,
            ])}
          />
        )}
      </DataSection>
    </div>
  );
}
