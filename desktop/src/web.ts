/* ── Web adapter: fetch HTTP → shared UI ────────────────────────
 * Thin adapter: provides Backend + auth flow, delegates UI to shared/ui.ts.
 */
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "../../shared/web/types.js";
import { init } from "../../shared/web/ui.js";

const NS = "/ctl";
const AUTH_NS = "/ctl/auth";
let authToken: string | null = localStorage.getItem("snet_token") || null;

/* ── fetch helpers ────────────────────────────────────────────── */
async function api(path: string, opts?: RequestInit): Promise<any> {
  const res = await fetch(NS + path, opts);
  const text = await res.text();
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    try { const j = JSON.parse(text); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  if (!text) return null;
  return JSON.parse(text);
}

async function authApi(path: string, opts?: RequestInit): Promise<any> {
  const headers: Record<string, string> = (opts?.headers as Record<string, string>) || {};
  if (authToken) headers["Authorization"] = "Bearer " + authToken;
  if (opts?.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
  const res = await fetch(AUTH_NS + path, { ...opts, headers });
  const text = await res.text();
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    try { const j = JSON.parse(text); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  if (!text) return null;
  return JSON.parse(text);
}

function jsonPost(path: string, body: Record<string, unknown>): RequestInit {
  return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

/* ── Auth flow ────────────────────────────────────────────────── */
async function checkAuth(): Promise<"no_password" | "needs_login" | "authenticated"> {
  try {
    const resp = await authApi("/check");
    if (!resp.hasPassword) return "no_password";
    if (resp.authenticated) return "authenticated";
    return "needs_login";
  } catch {
    return "needs_login";
  }
}

async function doLogin(password: string, remember: boolean): Promise<void> {
  const resp = await authApi("/login", { method: "POST", body: JSON.stringify({ password, remember }) });
  authToken = resp.token;
  localStorage.setItem("snet_token", authToken!);
}

async function doLogout(): Promise<void> {
  try { await authApi("/logout", { method: "POST" }); } catch {}
  authToken = null;
  localStorage.removeItem("snet_token");
}

async function doSetPassword(current: string, newPass: string): Promise<void> {
  await authApi("/password", { method: "POST", body: JSON.stringify({ current, new: newPass }) });
}

/* ── Page routing ─────────────────────────────────────────────── */
function showPage(id: string) {
  for (const p of ["app", "login-page", "setup-page", "onboarding-page"]) {
    const el = document.getElementById(p);
    if (el) el.hidden = (p !== id);
  }
}

/* ── Backend implementation ───────────────────────────────────── */
const backend: Backend = {
  hasDaemonControl: false,

  async status(): Promise<DaemonStatus | null> {
    try { return await api("/status"); } catch { return null; }
  },

  async create(params: { server: string; port: number; ca: string; name: string; subnet: string; approvalRequired: boolean; description?: string; tags?: string[]; visibility?: string }): Promise<CreateResp> {
    return api("/create", jsonPost("/create", {
      name: params.name, subnet: params.subnet, port: params.port,
      ca: params.ca, approvalRequired: params.approvalRequired,
      description: params.description ?? "", tags: params.tags ?? [], visibility: params.visibility ?? "",
    }));
  },

  async join(params: { server: string; port: number; ca: string; link: string }): Promise<JoinResp> {
    return api("/join", jsonPost("/join", {
      link: params.link, port: params.port, ca: params.ca,
    }));
  },

  async bind(params: { server: string; ca: string; code: string }): Promise<void> {
    await api("/bind", jsonPost("/bind", { server: params.server, ca: params.ca, code: params.code }));
  },

  async rejoin(nid: string): Promise<void> { await api("/rejoin", jsonPost("/rejoin", { nid })); },
  async leave(nid: string): Promise<void> { await api("/leave", jsonPost("/leave", { nid })); },
  async remove(nid: string): Promise<void> { await api("/remove", jsonPost("/remove", { nid })); },
  async deleteNet(nid: string): Promise<void> { await api("/delete", jsonPost("/delete", { nid })); },

  async netinfo(nid: string): Promise<NetInfoDetail> { return api("/netinfo?nid=" + encodeURIComponent(nid)); },
  async peers(nid: string): Promise<PeersResp> { return api("/peers?nid=" + encodeURIComponent(nid)); },

  async updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null; description?: string; tags?: string[]; visibility?: string }): Promise<void> {
    await api("/settings", jsonPost("/settings", {
      nid: params.nid, name: params.name, subnet: params.subnet, approvalRequired: params.approvalRequired,
      description: params.description ?? "", tags: params.tags ?? [], visibility: params.visibility ?? "",
    }));
  },

  async setRole(params: { nid: string; nodeId: string; role: string }): Promise<void> {
    await api("/role", jsonPost("/role", { nid: params.nid, nodeId: params.nodeId, role: params.role }));
  },

  async updateSubnets(params: { nid: string; subnets: string[] }): Promise<void> {
    await api("/subnets", jsonPost("/subnets", { nid: params.nid, subnets: params.subnets }));
  },

  async kick(params: { nid: string; nodeId: string }): Promise<void> {
    await api("/kick", jsonPost("/kick", { nid: params.nid, nodeId: params.nodeId }));
  },

  async approve(params: { nid: string; pendingId: string }): Promise<void> {
    await api("/approve", jsonPost("/approve", { nid: params.nid, pendingId: params.pendingId }));
  },

  async deny(params: { nid: string; pendingId: string }): Promise<void> {
    await api("/deny", jsonPost("/deny", { nid: params.nid, pendingId: params.pendingId }));
  },

  async cancelPending(pendingId: string): Promise<void> {
    await api("/cancel-pending", jsonPost("/cancel-pending", { pendingId }));
  },

  async resetCode(nid: string): Promise<{ pairingCode: string }> {
    return api("/reset-code", jsonPost("/reset-code", { nid }));
  },

  async detectLocalSubnets(): Promise<string[]> {
    const resp = await api("/local-subnets");
    return resp?.subnets ?? (Array.isArray(resp) ? resp : []);
  },

  async ensureDaemon(): Promise<void> {
    throw new Error("Web 版无法管理后台服务，请手动启动 snetd");
  },
};

/* ── Bootstrap ────────────────────────────────────────────────── */
function startApp() {
  showPage("app");
  init(backend);
}

document.addEventListener("DOMContentLoaded", () => {
  // Login form
  const loginSubmit = document.getElementById("login-submit");
  if (loginSubmit) loginSubmit.addEventListener("click", async () => {
    const pw = (document.getElementById("login-password") as HTMLInputElement)?.value;
    const remember = (document.getElementById("login-remember") as HTMLInputElement)?.checked ?? true;
    const result = document.getElementById("login-result")!;
    if (!pw) { result.className = "msg"; result.textContent = "请输入密码"; return; }
    (loginSubmit as HTMLButtonElement).disabled = true;
    result.className = "msg"; result.textContent = "正在登录…";
    try {
      await doLogin(pw, remember);
      startApp();
    } catch (e) {
      result.className = "msg"; result.textContent = "登录失败: " + e;
      (loginSubmit as HTMLButtonElement).disabled = false;
    }
  });
  document.getElementById("login-password")?.addEventListener("keydown", (e) => { if (e.key === "Enter") loginSubmit?.click(); });

  // Setup form
  const setupSubmit = document.getElementById("setup-submit");
  if (setupSubmit) setupSubmit.addEventListener("click", async () => {
    const pw = (document.getElementById("setup-password") as HTMLInputElement)?.value;
    const pw2 = (document.getElementById("setup-password2") as HTMLInputElement)?.value;
    const result = document.getElementById("setup-result")!;
    if (!pw || pw.length < 8) { result.className = "msg"; result.textContent = "密码至少需要 8 个字符"; return; }
    if (pw !== pw2) { result.className = "msg"; result.textContent = "两次输入不一致"; return; }
    (setupSubmit as HTMLButtonElement).disabled = true;
    result.className = "msg"; result.textContent = "正在设置…";
    try {
      await doSetPassword("", pw);
      await doLogin(pw, true);
      startApp();
    } catch (e) {
      result.className = "msg"; result.textContent = "设置失败: " + e;
      (setupSubmit as HTMLButtonElement).disabled = false;
    }
  });
  document.getElementById("setup-password2")?.addEventListener("keydown", (e) => { if (e.key === "Enter") setupSubmit?.click(); });

  // Onboarding bind
  const onbBind = document.getElementById("onb-bind");
  if (onbBind) onbBind.addEventListener("click", async () => {
    const server = (document.getElementById("onb-server") as HTMLInputElement)?.value.trim();
    const code = (document.getElementById("onb-code") as HTMLInputElement)?.value.trim();
    const result = document.getElementById("onb-bind-result")!;
    if (!server || !code) { result.className = "msg"; result.textContent = "请填写服务器地址和授权码"; return; }
    result.className = "msg"; result.textContent = "正在绑定…";
    try {
      await backend.bind({ server, ca: "", code });
      result.className = "msg ok"; result.textContent = "已绑定 " + server;
      startApp();
    } catch (e) { result.className = "msg"; result.textContent = "绑定失败: " + e; }
  });

  const onbSkip = document.getElementById("onb-skip");
  if (onbSkip) onbSkip.addEventListener("click", (e) => {
    e.preventDefault();
    startApp();
  });

  // Password visibility toggles
  document.querySelectorAll(".toggle-pw").forEach((btn) => {
    btn.addEventListener("click", () => {
      const input = document.getElementById((btn as HTMLElement).dataset.target!);
      if (!input) return;
      const show = (input as HTMLInputElement).type === "password";
      (input as HTMLInputElement).type = show ? "text" : "password";
      btn.innerHTML = show
        ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M17.94 17.94A10.07 10.07 0 0112 20c-7 0-11-8-11-8a18.45 18.45 0 015.06-5.94M9.9 4.24A9.12 9.12 0 0112 4c7 0 11 8 11 8a18.5 18.5 0 01-2.16 3.19m-6.72-1.07a3 3 0 11-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>'
        : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>';
    });
  });

  // Logout (listen for shared UI event)
  window.addEventListener("snet-logout", async () => {
    await doLogout();
    showPage("login-page");
  });
  document.getElementById("btn-logout")?.addEventListener("click", async () => {
    await doLogout();
    showPage("login-page");
  });

  // Initial auth check
  (async () => {
    const authState = await checkAuth();
    if (authState === "no_password") { showPage("setup-page"); return; }
    if (authState === "needs_login") { showPage("login-page"); return; }
    startApp();
  })();
});
