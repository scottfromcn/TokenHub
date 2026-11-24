import { BadgeDollarSign, Plus, Wallet } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { type AdminUser, type ApiContext, type AppData } from "../core/types";
import { type Wallet as WalletDTO, type WalletTransaction } from "../core/wallet";
import { formatTime } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";

type AdminWallet = WalletDTO & { owner_name?: string };

const walletOwnerKinds = [
  { value: "user", label: "用户" },
  { value: "project", label: "项目" },
] as const;

function walletUSD(value: number) {
  return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", maximumFractionDigits: 6 }).format(value);
}

function walletOwnerLabel(kind: string) {
  return kind === "user" ? tx("用户") : tx("项目");
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

export function WalletsView({ api, data, user }: { api: ApiContext; data: AppData; user: AdminUser }) {
  const [wallets, setWallets] = useState<AdminWallet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [kindFilter, setKindFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const [createKind, setCreateKind] = useState<string>("user");
  const [createOwner, setCreateOwner] = useState("");
  const [createAmount, setCreateAmount] = useState("");
  const [createNote, setCreateNote] = useState("");
  const [createError, setCreateError] = useState("");
  const [busy, setBusy] = useState(false);
  const [adjusting, setAdjusting] = useState<AdminWallet | null>(null);
  const [adjustAmount, setAdjustAmount] = useState("");
  const [adjustNote, setAdjustNote] = useState("");
  const [txnsWallet, setTxnsWallet] = useState<AdminWallet | null>(null);
  const [transactions, setTransactions] = useState<WalletTransaction[]>([]);
  const [txnsLoading, setTxnsLoading] = useState(false);

  const ownerOptions = useMemo(() => {
    if (createKind === "user") {
      return data.users.map((item) => ({ value: item.id, label: item.username || item.email }));
    }
    return data.projects.map((item) => ({ value: item.id, label: item.name || item.id }));
  }, [createKind, data.users, data.projects]);

  const load = useCallback(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    adminFetch(api, "/api/admin/wallets", { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("钱包列表加载失败")));
        return await response.json() as { data: AdminWallet[] };
      })
      .then((payload) => setWallets(payload.data ?? []))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setError(reason instanceof Error ? reason.message : tx("钱包列表加载失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api]);

  useEffect(() => load(), [load]);

  useEffect(() => {
    if (!txnsWallet) return;
    const controller = new AbortController();
    setTxnsLoading(true);
    adminFetch(api, `/api/admin/wallets/${encodeURIComponent(txnsWallet.id)}/transactions?limit=200`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("流水加载失败")));
        return await response.json() as { data: WalletTransaction[] };
      })
      .then((payload) => setTransactions(payload.data ?? []))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setTransactions([]);
      })
      .finally(() => {
        if (!controller.signal.aborted) setTxnsLoading(false);
      });
    return () => controller.abort();
  }, [api, txnsWallet]);

  async function submitCreate() {
    setCreateError("");
    const amount = Number(createAmount);
    if (!createOwner) {
      setCreateError(tx("请选择钱包归属"));
      return;
    }
    if (createAmount !== "" && (!Number.isFinite(amount) || amount === 0)) {
      setCreateError(tx("初始金额必须是有效数字"));
      return;
    }
    setBusy(true);
    try {
      const response = await adminFetch(api, "/api/admin/wallets", {
        method: "POST",
        body: JSON.stringify({
          owner_kind: createKind,
          owner_id: createOwner,
          ...(createAmount !== "" ? { amount_usd: amount } : {}),
          note: createNote,
        }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("创建钱包失败")));
      setCreating(false);
      setCreateOwner("");
      setCreateAmount("");
      setCreateNote("");
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setCreateError(reason instanceof Error ? reason.message : tx("创建钱包失败"));
    } finally {
      setBusy(false);
    }
  }

  async function submitAdjust() {
    if (!adjusting) return;
    setCreateError("");
    const amount = Number(adjustAmount);
    if (!Number.isFinite(amount) || amount === 0) {
      setCreateError(tx("调账金额必须是有效数字"));
      return;
    }
    setBusy(true);
    try {
      const response = await adminFetch(api, `/api/admin/wallets/${encodeURIComponent(adjusting.id)}/adjust`, {
        method: "POST",
        body: JSON.stringify({ amount_usd: amount, note: adjustNote }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("调账失败")));
      setAdjusting(null);
      setAdjustAmount("");
      setAdjustNote("");
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setCreateError(reason instanceof Error ? reason.message : tx("调账失败"));
    } finally {
      setBusy(false);
    }
  }

  async function toggleExempt(wallet: AdminWallet) {
    setBusy(true);
    try {
      const response = await adminFetch(api, `/api/admin/wallets/${encodeURIComponent(wallet.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ exempt: !wallet.exempt }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("更新钱包失败")));
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setError(reason instanceof Error ? reason.message : tx("更新钱包失败"));
    } finally {
      setBusy(false);
    }
  }

  const filtered = wallets.filter((wallet) => (kindFilter ? wallet.owner_kind === kindFilter : true));
  const canWrite = ["admin", "system_admin", "team_leader"].includes(String(user.role || "").toLowerCase());

  return (
    <div>
      <header className="page-header">
        <div className="page-context-header">
          <div className="page-context-main">
            <p className="eyebrow">{tx("预充值")}</p>
            <h1>{tx("预充值钱包")}</h1>
          </div>
          <div className="page-context-side">
            <select className="search-box" value={kindFilter} onChange={(event) => setKindFilter(event.target.value)} aria-label={tx("按归属筛选")}>
              <option value="">{tx("全部归属")}</option>
              {walletOwnerKinds.map((item) => (
                <option key={item.value} value={item.value}>{item.label}</option>
              ))}
            </select>
            {canWrite ? (
              <button className="button" type="button" onClick={() => { setCreating(true); setCreateError(""); }}>
                <Plus size={15} />{tx("新建钱包")}
              </button>
            ) : null}
          </div>
        </div>
        <p className="page-desc">{tx("to C 模式下余额耗尽将拦截新请求；豁免标记用于让内部用户或项目跳过余额管控。")}</p>
      </header>

      {error ? <div className="status-line error">{error}</div> : null}

      <DataSection title={formatTranslationTemplate(tx("钱包列表（{count}）"), { count: String(filtered.length) })}>
        {loading ? <p className="page-desc">{tx("加载中…")}</p> : filtered.length === 0 ? (
          <p className="page-desc">{tx("暂无钱包，点击右上角新建。")}</p>
        ) : (
          <SimpleTable
            columns={[tx("归属"), tx("名称"), tx("余额"), tx("豁免"), tx("更新时间"), tx("操作")]}
            rows={filtered.map((wallet) => [
              <span key="kind" className="pill">{walletOwnerLabel(wallet.owner_kind)}</span>,
              <span key="owner">{wallet.owner_name || wallet.owner_id}</span>,
              <strong key="balance" style={{ color: wallet.balance_usd > 0 ? undefined : "var(--red)" }}>{walletUSD(wallet.balance_usd)}</strong>,
              wallet.exempt ? <StatusPill key="exempt" status="active" label={tx("已豁免")} /> : <span key="exempt" className="pill">{tx("受管控")}</span>,
              <span key="time">{wallet.updated_at ? formatTime(wallet.updated_at) : "-"}</span>,
              <span key="actions" style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
                <button className="secondary-button" type="button" disabled={busy || !canWrite} onClick={() => { setAdjusting(wallet); setAdjustAmount(""); setAdjustNote(""); setCreateError(""); }}>
                  <BadgeDollarSign size={14} />{tx("调账")}
                </button>
                <button className="text-button" type="button" disabled={busy || !canWrite} onClick={() => toggleExempt(wallet)}>
                  {wallet.exempt ? tx("取消豁免") : tx("设为豁免")}
                </button>
                <button className="text-button" type="button" onClick={() => setTxnsWallet(wallet)}>
                  <Wallet size={14} />{tx("流水")}
                </button>
              </span>,
            ])}
          />
        )}
      </DataSection>

      {creating ? (
        <div className="modal-backdrop" onClick={(event) => { if (event.target === event.currentTarget) setCreating(false); }}>
          <div className="modal" role="dialog" aria-modal="true" aria-label={tx("新建钱包")}>
            <header className="modal-header">
              <h2>{tx("新建钱包")}</h2>
              <button className="icon-button" type="button" aria-label={tx("关闭")} onClick={() => setCreating(false)}>×</button>
            </header>
            <div className="modal-body">
              <label className="field">
                <span>{tx("归属类型")}</span>
                <select
                  value={createKind}
                  onChange={(event) => { setCreateKind(event.target.value); setCreateOwner(""); }}
                >
                  {walletOwnerKinds.map((item) => (
                    <option key={item.value} value={item.value}>{item.label}</option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>{tx("归属对象")}</span>
                <select value={createOwner} onChange={(event) => setCreateOwner(event.target.value)}>
                  <option value="">{tx("请选择")}</option>
                  {ownerOptions.map((item) => (
                    <option key={item.value} value={item.value}>{item.label}</option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>{tx("初始金额（USD，可为空）")}</span>
                <input value={createAmount} onChange={(event) => setCreateAmount(event.target.value)} placeholder="10" inputMode="decimal" />
              </label>
              <label className="field">
                <span>{tx("备注")}</span>
                <input value={createNote} onChange={(event) => setCreateNote(event.target.value)} />
              </label>
              {createError ? <div className="status-line error">{createError}</div> : null}
            </div>
            <footer className="modal-actions">
              <button className="secondary-button" type="button" onClick={() => setCreating(false)}>{tx("取消")}</button>
              <button className="button" type="button" disabled={busy} onClick={submitCreate}>{tx("创建")}</button>
            </footer>
          </div>
        </div>
      ) : null}

      {adjusting ? (
        <div className="modal-backdrop" onClick={(event) => { if (event.target === event.currentTarget) setAdjusting(null); }}>
          <div className="modal" role="dialog" aria-modal="true" aria-label={tx("钱包调账")}>
            <header className="modal-header">
              <h2>{tx("钱包调账")}</h2>
              <button className="icon-button" type="button" aria-label={tx("关闭")} onClick={() => setAdjusting(null)}>×</button>
            </header>
            <div className="modal-body">
              <p className="page-desc">
                {formatTranslationTemplate(tx("当前余额：{balance}"), { balance: walletUSD(adjusting.balance_usd) })}
                {" · "}
                {adjusting.owner_name || adjusting.owner_id}
              </p>
              <label className="field">
                <span>{tx("金额（USD，负数为扣减）")}</span>
                <input value={adjustAmount} onChange={(event) => setAdjustAmount(event.target.value)} placeholder="10 / -2.5" inputMode="decimal" />
              </label>
              <label className="field">
                <span>{tx("备注")}</span>
                <input value={adjustNote} onChange={(event) => setAdjustNote(event.target.value)} />
              </label>
              {createError ? <div className="status-line error">{createError}</div> : null}
            </div>
            <footer className="modal-actions">
              <button className="secondary-button" type="button" onClick={() => setAdjusting(null)}>{tx("取消")}</button>
              <button className="button" type="button" disabled={busy} onClick={submitAdjust}>{tx("确认调账")}</button>
            </footer>
          </div>
        </div>
      ) : null}

      {txnsWallet ? (
        <div className="modal-backdrop" onClick={(event) => { if (event.target === event.currentTarget) setTxnsWallet(null); }}>
          <div className="modal" role="dialog" aria-modal="true" aria-label={tx("钱包流水")}>
            <header className="modal-header">
              <h2>{tx("钱包流水")}</h2>
              <button className="icon-button" type="button" aria-label={tx("关闭")} onClick={() => setTxnsWallet(null)}>×</button>
            </header>
            <div className="modal-body">
              {txnsLoading ? (
                <p className="page-desc">{tx("加载中…")}</p>
              ) : transactions.length === 0 ? (
                <p className="page-desc">{tx("暂无流水")}</p>
              ) : (
                <SimpleTable
                  columns={[tx("时间"), tx("类型"), tx("金额"), tx("余额") , tx("关联")]}
                  rows={transactions.map((txn) => [
                    <span key="t">{formatTime(txn.created_at)}</span>,
                    <span key="type">{walletTxnTypeLabel(txn.type)}</span>,
                    <strong key="amt" style={{ color: txn.amount_usd >= 0 ? "var(--pos)" : undefined }}>{walletUSD(txn.amount_usd)}</strong>,
                    <span key="after">{walletUSD(txn.balance_after_usd)}</span>,
                    <span key="ref">{txn.ref_id || txn.note || "-"}</span>,
                  ])}
                />
              )}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
