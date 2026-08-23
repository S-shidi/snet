/* ── Android adapter: WebBridge JNI → shared UI ────────────────── */
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "../../shared/web/types.js";
import { init } from "../../shared/web/ui.js";

declare global {
  interface Window {
    WebBridge: {
      status(): string;
      create(params: string): string;
      join(params: string): string;
      bind(params: string): string;
      rejoin(nid: string): string;
      leave(nid: string): string;
      remove(nid: string): string;
      deleteNet(nid: string): string;
      netinfo(nid: string): string;
      peers(nid: string): string;
      updateSettings(params: string): string;
      updateSubnets(params: string): string;
      kick(params: string): string;
      approve(params: string): string;
      deny(params: string): string;
      cancelPending(pendingId: string): string;
      resetCode(nid: string): string;
      detectLocalSubnets(): string;
      ensureDaemon(): string;
      startVpn(): string;
      stopVpn(): string;
      getDeviceId(): string;
    };
  }
}

function call<T>(fn: string, ...args: unknown[]): T {
  const result = (window.WebBridge as any)[fn](...args);
  return JSON.parse(result);
}

const backend: Backend = {
  hasDaemonControl: true,

  async status(): Promise<DaemonStatus | null> {
    try {
      const raw = window.WebBridge.status();
      return JSON.parse(raw);
    } catch {
      return null;
    }
  },

  async create(params: { server: string; port: number; ca: string; name: string; subnet: string; approvalRequired: boolean }): Promise<CreateResp> {
    return call<CreateResp>("create", JSON.stringify({
      server: params.server,
      port: params.port,
      ca: params.ca,
      name: params.name,
      subnet: params.subnet,
      approvalRequired: params.approvalRequired,
    }));
  },

  async join(params: { server: string; port: number; ca: string; link: string }): Promise<JoinResp> {
    return call<JoinResp>("join", JSON.stringify({
      server: params.server,
      port: params.port,
      ca: params.ca,
      link: params.link,
    }));
  },

  async bind(params: { server: string; ca: string; code: string }): Promise<void> {
    call("bind", JSON.stringify({
      server: params.server,
      ca: params.ca,
      code: params.code,
    }));
  },

  async rejoin(nid: string): Promise<void> { call("rejoin", nid); },
  async leave(nid: string): Promise<void> { call("leave", nid); },
  async remove(nid: string): Promise<void> { call("remove", nid); },
  async deleteNet(nid: string): Promise<void> { call("deleteNet", nid); },

  async netinfo(nid: string): Promise<NetInfoDetail> {
    return call<NetInfoDetail>("netinfo", nid);
  },

  async peers(nid: string): Promise<PeersResp> {
    return call<PeersResp>("peers", nid);
  },

  async updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null }): Promise<void> {
    call("updateSettings", JSON.stringify({
      nid: params.nid,
      name: params.name,
      subnet: params.subnet,
      approvalRequired: params.approvalRequired,
    }));
  },

  async updateSubnets(params: { nid: string; subnets: string[] }): Promise<void> {
    call("updateSubnets", JSON.stringify({
      nid: params.nid,
      subnets: JSON.stringify(params.subnets),
    }));
  },

  async kick(params: { nid: string; nodeId: string }): Promise<void> {
    call("kick", JSON.stringify({
      nid: params.nid,
      nodeId: params.nodeId,
    }));
  },

  async approve(params: { nid: string; pendingId: string }): Promise<void> {
    call("approve", JSON.stringify({
      nid: params.nid,
      pendingId: params.pendingId,
    }));
  },

  async deny(params: { nid: string; pendingId: string }): Promise<void> {
    call("deny", JSON.stringify({
      nid: params.nid,
      pendingId: params.pendingId,
    }));
  },

  async cancelPending(pendingId: string): Promise<void> {
    call("cancelPending", pendingId);
  },

  async resetCode(nid: string): Promise<{ pairingCode: string }> {
    return call<{ pairingCode: string }>("resetCode", nid);
  },

  async detectLocalSubnets(): Promise<string[]> {
    return call<string[]>("detectLocalSubnets");
  },

  async ensureDaemon(): Promise<void> {
    call("ensureDaemon");
  },
};

function showApp(): void {
  const app = document.getElementById("app");
  const login = document.getElementById("login-page");
  const setup = document.getElementById("setup-page");
  const onb = document.getElementById("onboarding-page");
  if (app) app.hidden = false;
  if (login) login.hidden = true;
  if (setup) setup.hidden = true;
  if (onb) onb.hidden = true;
}

// Wait for DOM ready, then show app and init
function boot() {
  showApp();
  init(backend);

  // Logout: reload to re-trigger device check
  document.getElementById("btn-logout")?.addEventListener("click", () => {
    if (confirm("确定退出当前设备绑定？")) window.location.reload();
  });
  window.addEventListener("snet-logout", () => {
    if (confirm("确定退出当前设备绑定？")) window.location.reload();
  });
}

if (document.readyState === "loading") {
  window.addEventListener("DOMContentLoaded", boot);
} else {
  boot();
}
