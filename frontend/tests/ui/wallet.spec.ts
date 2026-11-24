import { test, expect, capture } from "./harness";
import { user } from "./fixtures/shell";

test.describe("my-wallet user portal", () => {
  test.use({ sessionUser: { ...user, role: "user", username: "ui-wallet-user" } });

  for (const scenario of ["redeem" as const, "already-used" as const]) {
    test(`my-wallet ${scenario}`, async ({ page, api }, testInfo) => {
      let balance = 2.5;
      const wallet = () => ({
        wallet: { id: "wal_ui", owner_kind: "user", owner_id: "usr_ui", balance_usd: balance, exempt: false },
        transactions: [
          { id: "wtx_1", owner_kind: "user", owner_id: "usr_ui", type: "debit", amount_usd: -0.5, balance_after_usd: 2.5, created_at: "2026-09-07T01:59:00Z" },
        ],
      });
      api.define("GET", "/api/admin/my/wallet", () => ({ json: wallet() }));
      api.define("POST", "/api/admin/my/wallet/redeem", input => {
        expect(input.body).toMatchObject({ code: scenario === "redeem" ? "thk_ui_fixture_code" : "thk_used_code" });
        if (scenario === "already-used") {
          return { status: 409, json: { error: { code: "topup_code_already_used", message: "Top-up code has already been used" } } };
        }
        balance = 12.5;
        return { json: { wallet: { id: "wal_ui", owner_kind: "user", owner_id: "usr_ui", balance_usd: 12.5, exempt: false }, face_value_usd: 10 } };
      });

      await page.goto("/my-wallet");
      await expect(page.getByRole("heading", { name: "我的钱包", exact: true })).toBeVisible();
      await expect(page.locator(".metric-value").first()).toHaveText(/2\.50/);

      await page.getByRole("textbox", { name: "兑换码" }).fill(scenario === "redeem" ? "thk_ui_fixture_code" : "thk_used_code");
      await page.getByRole("button", { name: "兑换", exact: true }).click();

      if (scenario === "redeem") {
        await expect(page.locator(".status-line.success")).toContainText(/10\.00/);
        await expect(page.locator(".metric-value").first()).toHaveText(/12\.50/);
        await capture(page, testInfo, page.locator(".content-panel"), "my-wallet-redeemed", "兑换码充值成功后的余额与流水");
      } else {
        await expect(page.locator(".status-line.error")).toContainText("already been used");
      }
    });
  }
});
