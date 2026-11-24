import { Copy, Download, Plus, Ticket } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { type ApiContext } from "../core/types";
import { type TopupCode } from "../core/wallet";
import { formatTime } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";

function walletUSD(value: number) {
  return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", maximumFractionDigits: 6 }).format(value);
}

function topupStatusLabel(status: string) {
  switch (status) {
    case "unused":
      return tx("未使用");
    case "used":
      return tx("已使用");
    case "revoked":
      return tx("已作废");
    default:
      return status;
  }
}

export function TopupCodesView({ api }: { api: ApiContext }) {
  const [codes, setCodes] = useState<TopupCode[]>([]);
  const [statusFilter, setStatusFilter] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [generating, setGenerating] = useState(false);
  const [count, setCount] = useState("10");
  const [faceValue, setFaceValue] = useState("10");
  const [expiresAt, setExpiresAt] = useState("");
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState("");
  const [busy, setBusy] = useState(false);
  const [issued, setIssued] = useState<{ batchID: string; codes: string[] } | null>(null);

  const load = useCallback(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    const query = new URLSearchParams();
    if (statusFilter) query.set("status", statusFilter);
    adminFetch(api, `/api/admin/topup-codes?${query.toString()}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("兑换码加载失败")));
        return await response.json() as { data: TopupCode[] };
      })
      .then((payload) => setCodes(payload.data ?? []))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setError(reason instanceof Error ? reason.message : tx("兑换码加载失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, statusFilter]);

  useEffect(() => load(), [load]);

  async function submitGenerate() {
    setFormError("");
    const parsedCount = Number(count);
    const parsedValue = Number(faceValue);
    if (!Number.isInteger(parsedCount) || parsedCount < 1 || parsedCount > 1000) {
      setFormError(tx("生成数量必须是 1-1000 的整数"));
      return;
    }
    if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
      setFormError(tx("面值必须是大于 0 的数字"));
      return;
    }
    setBusy(true);
    try {
      const response = await adminFetch(api, "/api/admin/topup-codes", {
        method: "POST",
        body: JSON.stringify({
          count: parsedCount,
          face_value_usd: parsedValue,
          ...(expiresAt ? { expires_at: new Date(expiresAt).toISOString() } : {}),
          note,
        }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("生成兑换码失败")));
      const payload = await response.json() as { batch_id: string; codes: string[] };
      setIssued({ batchID: payload.batch_id, codes: payload.codes ?? [] });
      setGenerating(false);
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setFormError(reason instanceof Error ? reason.message : tx("生成兑换码失败"));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(code: TopupCode) {
    if (!window.confirm(tx("确认作废该兑换码？作废后不可再兑换。"))) return;
    setBusy(true);
    try {
      const response = await adminFetch(api, `/api/admin/topup-codes/${encodeURIComponent(code.id)}/revoke`, { method: "POST" });
      if (!response.ok) throw new Error(await readAdminError(response, tx("作废失败")));
      load();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setError(reason instanceof Error ? reason.message : tx("作废失败"));
    } finally {
      setBusy(false);
    }
  }

  function copyIssued() {
    if (!issued) return;
    void navigator.clipboard?.writeText(issued.codes.join("\n"));
  }

  function downloadIssued() {
    if (!issued) return;
    const blob = new Blob([issued.codes.join("\n") + "\n"], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `tokenhub-topup-${issued.batchID}.txt`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div>
      <header className="page-header">
        <div className="page-context-header">
          <div className="page-context-main">
            <p className="eyebrow">{tx("预充值")}</p>
            <h1>{tx("充值兑换码")}</h1>
          </div>
          <div className="page-context-side">
            <select className="search-box" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)} aria-label={tx("按状态筛选")}>
              <option value="">{tx("全部状态")}</option>
              <option value="unused">{tx("未使用")}</option>
              <option value="used">{tx("已使用")}</option>
              <option value="revoked">{tx("已作废")}</option>
            </select>
            <button className="button" type="button" onClick={() => { setGenerating(true); setFormError(""); }}>
              <Plus size={15} />{tx("批量生成")}
            </button>
          </div>
        </div>
        <p className="page-desc">{tx("兑换码一次性使用，完整码只在生成时显示一次；用户在「我的钱包」中兑换入账。")}</p>
      </header>

      {error ? <div className="status-line error">{error}</div> : null}

      {issued ? (
        <DataSection title={formatTranslationTemplate(tx("批次 {batch} 的完整兑换码（仅此一次显示）"), { batch: issued.batchID })}>
          <div style={{ display: "flex", gap: 8, marginBottom: 10, flexWrap: "wrap" }}>
            <button className="button" type="button" onClick={copyIssued}><Copy size={14} />{tx("复制全部")}</button>
            <button className="secondary-button" type="button" onClick={downloadIssued}><Download size={14} />{tx("下载清单")}</button>
            <button className="text-button" type="button" onClick={() => setIssued(null)}>{tx("我已保存，关闭")}</button>
          </div>
          <div className="gateway-code-block"><pre>{issued.codes.join("\n")}</pre></div>
        </DataSection>
      ) : null}

      <DataSection title={formatTranslationTemplate(tx("兑换码记录（{count}）"), { count: String(codes.length) })}>
        {loading ? (
          <p className="page-desc">{tx("加载中…")}</p>
        ) : (
          <SimpleTable
            paginationKey="topup-codes"
            columns={[tx("批次"), tx("前缀"), tx("面值"), tx("状态"), tx("使用者"), tx("过期时间"), tx("备注"), tx("操作")]}
            rows={codes.map((code) => [
              <span key="batch" title={code.batch_id}>{code.batch_id.slice(0, 12)}…</span>,
              <span key="prefix">{code.code_prefix}…</span>,
              <strong key="value">{walletUSD(code.face_value_usd)}</strong>,
              <StatusPill key="status" status={code.status === "unused" ? "active" : code.status === "used" ? "warn" : "error"} label={topupStatusLabel(code.status)} />,
              <span key="user">{code.used_by_user_id ? formatTime(code.used_at ?? code.created_at) : "-"}</span>,
              <span key="exp">{code.expires_at ? formatTime(code.expires_at) : tx("永久")}</span>,
              <span key="note">{code.note || "-"}</span>,
              <button key="revoke" className="text-button" type="button" disabled={busy || code.status !== "unused"} onClick={() => revoke(code)}>
                <Ticket size={14} />{tx("作废")}
              </button>,
            ])}
          />
        )}
      </DataSection>

      {generating ? (
        <div className="modal-backdrop" onClick={(event) => { if (event.target === event.currentTarget) setGenerating(false); }}>
          <form className="modal" role="dialog" aria-modal="true" aria-label={tx("批量生成兑换码")} onSubmit={(event) => { event.preventDefault(); void submitGenerate(); }}>
            <div className="modal-header">
              <div>
                <p className="eyebrow">{tx("预充值")}</p>
                <h2>{tx("批量生成兑换码")}</h2>
              </div>
              <button className="icon-button" type="button" aria-label={tx("关闭")} onClick={() => setGenerating(false)}>×</button>
            </div>
            <div className="modal-body">
              <label className="field">
                <span>{tx("数量（1-1000）")}</span>
                <input value={count} onChange={(event) => setCount(event.target.value)} inputMode="numeric" />
              </label>
              <label className="field">
                <span>{tx("面值（USD）")}</span>
                <input value={faceValue} onChange={(event) => setFaceValue(event.target.value)} inputMode="decimal" placeholder="10" />
              </label>
              <label className="field">
                <span>{tx("过期时间（可选）")}</span>
                <input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} />
              </label>
              <label className="field">
                <span>{tx("批次备注")}</span>
                <input value={note} onChange={(event) => setNote(event.target.value)} placeholder={tx("例如：双十一活动")} />
              </label>
              {formError ? <div className="status-line error">{formError}</div> : null}
            </div>
            <div className="modal-actions">
              <button className="secondary-button" type="button" onClick={() => setGenerating(false)}>{tx("取消")}</button>
              <button className="button" type="submit" disabled={busy}>{tx("生成")}</button>
            </div>
          </form>
        </div>
      ) : null}
    </div>
  );
}
