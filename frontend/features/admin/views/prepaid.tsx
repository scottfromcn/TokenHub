import { type AdminUser, type ApiContext, type AppData, type ViewKey } from "../core/types";
import { MyWalletView } from "./my-wallet";
import { TopupCodesView } from "./topup-codes";
import { WalletsView } from "./wallets";

export type PrepaidViewKey = "wallets" | "topup-codes" | "my-wallet";

export function PrepaidViews({ view, api, data, user }: { view: ViewKey; api: ApiContext; data: AppData; user: AdminUser }) {
  if (view === "wallets") return <WalletsView api={api} data={data} user={user} />;
  if (view === "topup-codes") return <TopupCodesView api={api} />;
  if (view === "my-wallet") return <MyWalletView api={api} user={user} />;
  return null;
}
