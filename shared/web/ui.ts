/* ── Shared UI logic for SNET client ──────────────────────────────
 * This module is the single source of truth for the client UI.
 * Platform adapters (Desktop/Web/Android) provide a Backend implementation
 * and call init() to wire everything up.
 */
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "./types.js";
import { $, esc, SPIN, fmtBytes, onlineCount, toast, copyText, renderQR, loadQR, openModal, closeModal, confirmDialog, initTabs } from "./utils.js";
import { subnetCheck, subnetWidgetHTML, subnetWidgetInit } from "./subnet.js";

// Optional hook: a platform adapter (Android WebView) can call refresh from
// native code after a background daemon op completes, to reconcile the
// optimistic switch quickly instead of waiting for the next poll.
(globalThis as any).snetRefresh = () => refresh();

let backend: Backend;
let status: DaemonStatus | null = null;
let onboardingShown = false;
let skipOnboarding = false;

/* ── Settings (localStorage) ──────────────────────────────────── */
const SETTINGS_KEY = "snet.settings";
const LEGACY_CA = "/usr/local/snet/certs/server.pem";
const DEFAULT_SETTINGS = { server: "https://snet.uizhi.eu.org:8090", ca: "", wgport: 51820 };
type Settings = typeof DEFAULT_SETTINGS;

function loadSettings(): Settings {
  let s: Settings;
  try {
    s = { ...DEFAULT_SETTINGS, ...JSON.parse(localStorage.getItem(SETTINGS_KEY) || "{}") };
  } catch {
    s = { ...DEFAULT_SETTINGS };
  }
  if (navigator.platform.toLowerCase().includes("mac") && s.ca === LEGACY_CA) {
    s.ca = "";
    saveSettings(s);
  }
  return s;
}
function saveSettings(s: Settings) {
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
}
function currentServer(): string {
  if (status?.bound) return status.serverAddr ?? "";
  return "";
}

/* ── Refresh ──────────────────────────────────────────────────── */
let refreshing = false;
let lastNetHTML = "";
let lastTunnelHTML = "";
let lastEmptyHTML = "";
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    status = await backend.status();
    await refreshOwnerInfo();
    await refreshPeerSubnets();
  } catch {
    status = null;
  }
  renderHeader();
  renderNetworks();
  renderStatus();
  maybeShowOnboarding();
  refreshing = false;
}

/* ── Owner info cache ─────────────────────────────────────────── */
const ownerInfo: Record<string, NetInfoDetail> = {};
let lastOwnerFetch = 0;
async function refreshOwnerInfo(force = false) {
  const now = Date.now();
  if (!force && now - lastOwnerFetch < 10000) return;
  lastOwnerFetch = now;
  if (!status) return;
  const ownerNets = (status.networks ?? []).filter((n) => n.owner);
  await Promise.all(
    ownerNets.map(async (n) => {
      try {
        ownerInfo[n.networkId] = await backend.netinfo(n.networkId);
      } catch {
        delete ownerInfo[n.networkId];
      }
    }),
  );
}

/* ── Peer advertised subnets (any member) ─────────────────────── */
const peerAllowed: Record<string, string[]> = {};
let lastPeerSubnetsFetch = 0;
async function refreshPeerSubnets(force = false) {
  const now = Date.now();
  if (!force && now - lastPeerSubnetsFetch < 10000) return;
  lastPeerSubnetsFetch = now;
  if (!status) return;
  await Promise.all(
    (status.networks ?? []).map(async (n) => {
      try {
        const r = await backend.peers(n.networkId);
        const set = new Set<string>();
        for (const p of r.peers ?? []) (p.allowedSubnets ?? []).forEach((s) => set.add(s));
        for (const s of n.allowedSubnets ?? []) set.add(s);
        peerAllowed[n.networkId] = [...set].sort();
      } catch {
        delete peerAllowed[n.networkId];
      }
    }),
  );
}

/* ── Header ───────────────────────────────────────────────────── */
function renderHeader() {
  const dot = $("#daemon-state") as HTMLElement;
  if (!dot) return;
  if (backend.hasDaemonControl) {
    if (status) {
      dot.className = "live-dot ok";
      dot.textContent = "运行中";
    } else {
      dot.className = "live-dot err";
      dot.textContent = "未运行";
    }
    const btn = $("#header-svc-btn") as HTMLButtonElement;
    if (btn) {
      btn.hidden = !!status;
      btn.disabled = false;
      if (!status) {
        btn.textContent = "启动";
        btn.title = "启动系统后台守护进程（可能弹出管理员密码框）";
        btn.onclick = () => void ensureDaemon();
      }
    }
  } else {
    // Web mode: always show running
    dot.className = "live-dot ok";
    dot.textContent = "snetd";
  }
}

/* ── Network cards ────────────────────────────────────────────── */
function netCard(n: { networkId: string; name?: string; ip?: string; subnet?: string; interface?: string; active?: boolean; owner?: boolean; serverState?: string; error?: string; peerStats?: Record<string, { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number }>; allowedSubnets?: string[]; visibility?: string; description?: string; tags?: string[]; role?: string }): string {
  const gone = n.serverState === "gone";
  const linked = !!n.interface;
  const state = gone ? "已删除" : linked ? "已链接" : n.active ? "未就绪" : "未链接";
  const cls = gone ? "gone" : linked ? "ok" : n.active ? "warn" : "off";
  const peers = n.peerStats ?? {};
  const total = Object.keys(peers).length;
  const detail = n.owner ? ownerInfo[n.networkId] : undefined;
  const memberTotal = detail ? detail.nodes?.length ?? total + 1 : total + 1;
  const memberOnline = detail
    ? (detail.nodes ?? []).filter((nd) => nd.online).length
    : onlineCount(n) + (linked ? 1 : 0);
  const pendingCount = detail?.pendingCount ?? detail?.pending?.length ?? 0;
  const sharePill = (n.visibility === "shareable") ? `<span class="pill share" title="可被社区发现">shareable</span>` : "";
  const memberLine = gone
    ? `<span class="muted">已失效</span>`
    : memberTotal <= 1
      ? `<span class="muted">暂无其他成员</span>`
      : `<button type="button" data-act="members" class="meta-link">成员 <b>${memberOnline}/${memberTotal}</b> 在线</button>`;
  const tx = Object.values(peers).reduce((a, p) => a + (p.TxBytes ?? 0), 0);
  const rx = Object.values(peers).reduce((a, p) => a + (p.RxBytes ?? 0), 0);
  const pendingPill = !gone && pendingCount > 0 ? `<button class="pill pending" data-act="show-pending" title="点击查看待批准请求">待批准 (${pendingCount})</button>` : "";
  const err = gone ? `<p class="msg">${esc(n.error || "该网络已在服务端被删除")}</p>` : n.error ? `<p class="msg">${esc(n.error)}</p>` : "";
  const allSubnets = peerAllowed[n.networkId] ?? n.allowedSubnets ?? [];
  return `
  <div class="net" data-nid="${esc(n.networkId)}" data-owner="${n.owner ? "1" : "0"}" ${gone ? 'data-gone="1"' : ""}>
    <div class="net-row">
      <div class="net-main">
        <div class="net-name">${esc(n.name || n.networkId)} ${n.owner ? `<span class="pill owner">owner</span>` : ""} ${sharePill} ${pendingPill}</div>
        <div class="net-meta">
          <span>IP <code>${esc(n.ip ?? "-")}</code></span>
          <span>网段 <code>${esc(n.subnet ?? "-")}</code></span>
          <span>ID <code>${esc(n.networkId)}</code></span>
          ${allSubnets.length > 0 ? `<span class="pill subnet-route" title="可路由：${esc(allSubnets.join(", "))}">路由 ${allSubnets.length} 个子网</span>` : ""}
        </div>
        <div class="net-stats">${memberLine}<span>收 <b>${fmtBytes(rx)}</b></span><span>发 <b>${fmtBytes(tx)}</b></span></div>
      </div>
      <div class="net-side">
        <span class="pill ${cls}">${state}</span>
        <label class="switch" title="${gone ? "该网络已删除" : linked ? "断开该网络" : "连接该网络"}">
          <input type="checkbox" data-act="toggle" ${linked ? "checked" : ""} ${gone ? "disabled" : ""} />
          <span class="slider"></span>
        </label>
        <div class="net-menu-wrap">
          <button class="net-more" data-act="more" title="更多操作" aria-haspopup="menu">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>
          </button>
        </div>
      </div>
    </div>
    ${err}
  </div>`;
}

/* ── Network card "more" menu ─────────────────────────────────── */
function buildNetMenu(nid: string, isOwner: boolean, gone: boolean): HTMLElement {
  const card = document.querySelector<HTMLElement>(`.net[data-nid="${CSS.escape(nid)}"]`);
  const wrap = card?.querySelector('.net-menu-wrap');
  if (!wrap) return document.createElement('div');
  wrap.querySelector('.net-menu')?.remove();
  const menu = document.createElement('div');
  menu.className = 'net-menu';
  menu.setAttribute('role', 'menu');
  const iconMembers = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>';
  const iconInvite = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="8.5" cy="7" r="4"/><line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/></svg>';
  const iconSettings = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>';
  const iconSubnets = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>';
  const iconCode = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>';
  const iconInfo = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>';
  const iconTrash = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>';
  const iconExit = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>';
  if (gone) {
    // The network is gone on the server; only allow the user to drop the
    // local record. Server-side ops are blocked by the action guard.
    menu.innerHTML = `
      <button type="button" data-act="delete" data-nid="${esc(nid)}" class="danger">${iconTrash} 删除本地记录</button>`;
  } else if (isOwner) {
    menu.innerHTML = `
      <button type="button" data-act="info" data-nid="${esc(nid)}">${iconInfo} 详情</button>
      <button type="button" data-act="members" data-nid="${esc(nid)}">${iconMembers} 查看成员</button>
      <button type="button" data-act="invite" data-nid="${esc(nid)}">${iconInvite} 邀请</button>
      <button type="button" data-act="settings" data-nid="${esc(nid)}">${iconSettings} 设置</button>
      <button type="button" data-act="subnets" data-nid="${esc(nid)}">${iconSubnets} 子网路由</button>
      <button type="button" data-act="code" data-nid="${esc(nid)}">${iconCode} 查看配对码</button>
      <hr/>
      <button type="button" data-act="delete" data-nid="${esc(nid)}" class="danger">${iconTrash} 删除网络</button>`;
  } else {
    menu.innerHTML = `
      <button type="button" data-act="subnets" data-nid="${esc(nid)}">${iconSubnets} 子网路由</button>
      <hr/>
      <button type="button" data-act="remove" data-nid="${esc(nid)}" class="danger">${iconExit} 退出网络</button>`;
  }
  wrap.appendChild(menu);
  const mr = menu.getBoundingClientRect();
  const btnEl = wrap.querySelector('.net-more');
  const btnTop = btnEl ? btnEl.getBoundingClientRect().top : 0;
  if (mr.bottom > window.innerHeight - 8 && mr.height < btnTop - 8) {
    menu.classList.add('up');
  }
  return menu;
}

function closeNetMenus() {
  document.querySelectorAll('.net-menu').forEach(m => m.remove());
  document.querySelectorAll('.net-more.open').forEach(b => b.classList.remove('open'));
}

function toggleNetMenu(btn: HTMLButtonElement, nid: string, gone: boolean) {
  const wrap = btn.closest('.net-menu-wrap');
  const isOpen = !!wrap?.querySelector('.net-menu');
  closeNetMenus();
  if (isOpen) return;
  btn.classList.add('open');
  const card = btn.closest<HTMLElement>('.net');
  const isOwner = card?.dataset.owner === "1";
  buildNetMenu(nid, isOwner, gone);
}

function renderNetworks() {
  const list = $("#net-list");
  if (!list) return;
  const nets = status?.networks ?? [];
  const pending = status?.pendingJoins ?? [];
  if (!status) {
    const html = `<div class="empty">
      <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M24 4l18 26H6L24 4z"/><path d="M24 34v6"/><path d="M12 46h24"/></svg>
      <div class="t">后台服务未运行</div>
      <div class="s">需要系统后台守护进程 snetd 维持网络隧道</div>
      <div class="empty-actions">${backend.hasDaemonControl ? `<button id="empty-start" class="btn">启动</button>` : ""}</div>
    </div>`;
    if (html !== lastEmptyHTML) {
      list.innerHTML = html;
      lastEmptyHTML = html;
      lastNetHTML = "";
      const startBtn = $("#empty-start");
      if (startBtn) startBtn.addEventListener("click", ensureDaemon);
    }
    return;
  }
  lastEmptyHTML = "";
  const hasOwner = nets.some((n) => n.owner);
  const btnCreate = $("#btn-create");
  if (btnCreate) {
    btnCreate.disabled = hasOwner;
    btnCreate.classList.toggle("muted", hasOwner);
  }
  if (!nets.length && !pending.length) {
    const html = `<div class="empty">
      <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="24" r="6"/><circle cx="36" cy="24" r="6"/><path d="M18 24h12"/></svg>
      <div class="t">还没有加入任何网络</div>
      <div class="s">创建一个网络，或使用邀请链接/配对码加入其他设备</div>
      <div class="empty-actions">
        <button id="empty-create" class="btn" ${hasOwner ? 'disabled data-disabled-reason="已创建网络，不支持再创建"' : ''}>创建网络</button>
        <button id="empty-join" class="btn ghost">加入网络</button>
      </div>
    </div>`;
    if (html !== lastEmptyHTML) {
      list.innerHTML = html;
      lastEmptyHTML = html;
      lastNetHTML = "";
      $("#empty-create")?.addEventListener("click", openCreateModal);
      $("#empty-join")?.addEventListener("click", openJoinModal);
    }
    return;
  }
  const sortedNets = [...nets].sort((a, b) => {
    const ta = a.joinedAt || "";
    const tb = b.joinedAt || "";
    if (ta && tb) return ta.localeCompare(tb);
    if (ta) return -1;
    if (tb) return 1;
    return (a.networkId || "").localeCompare(b.networkId || "");
  });
  const netHTML = sortedNets.map(netCard).join("");
  const pendingHTML = pending.length
    ? `<h4 class="pending-heading">待批准 · 加入请求</h4>` +
      pending.map(
        (p) => `<div class="net pending" data-pid="${esc(p.pendingId)}" data-nid="${esc(p.networkId)}">
        <div class="net-row">
          <div class="net-main">
            <div class="net-name">${esc(p.networkId)} <span class="pill warn">待批准</span></div>
            <div class="net-meta">${p.error ? `<span class="muted">${esc(p.error)}</span>` : "等待网络创建者批准加入请求"}</div>
          </div>
          <div class="net-side">
            <button data-act="cancel-pending" class="btn danger ghost sm">取消请求</button>
          </div>
        </div>
      </div>`,
      ).join("")
    : "";
  const combinedHTML = netHTML + pendingHTML;
  if (combinedHTML !== lastNetHTML) {
    list.innerHTML = combinedHTML;
    lastNetHTML = combinedHTML;
  }
  const cnt = $("#cnt-net");
  if (cnt) {
    cnt.textContent = String(nets.length);
    cnt.className = "cnt" + (pending.length ? " hot" : "");
    cnt.title = pending.length ? `${pending.length} 个待批准请求` : "";
  }
}

/* ── Network card events (delegation) ─────────────────────────── */
function bindNetListEvents() {
  const netList = $("#net-list");
  if (!netList) return;

  netList.addEventListener("click", async (e) => {
    const target = e.target as HTMLElement;
    // Handle "more" button
    const moreBtn = target.closest<HTMLButtonElement>(".net-more");
    if (moreBtn) {
      e.stopPropagation();
      const card = moreBtn.closest<HTMLElement>(".net");
      if (card) toggleNetMenu(moreBtn, card.dataset.nid!, card.dataset.gone === "1");
      return;
    }
    // Handle menu item clicks
    const menuBtn = target.closest<HTMLButtonElement>(".net-menu button[data-act]");
    if (menuBtn) {
      e.stopPropagation();
      const nid = menuBtn.dataset.nid || menuBtn.closest<HTMLElement>(".net")?.dataset.nid;
      const pid = menuBtn.closest<HTMLElement>(".net")?.dataset.pid;
      closeNetMenus();
      if (!nid) return;
      const act = menuBtn.dataset.act!;
      if (act === "cancel-pending") {
        if (!(await confirmDialog("取消加入请求", "取消等待批准？", true))) return;
        try {
          await backend.cancelPending(pid!);
          toast("已取消加入请求");
        } catch (err) {
          toast(String(err), "err");
        }
        await refresh();
        return;
      }
      if (act === "show-pending") {
        await showPending(nid);
        return;
      }
      await onAction(act, nid, menuBtn);
      return;
    }
    // Handle pending card cancel button
    const actEl = target.closest<HTMLElement>("[data-act]");
    if (!actEl) return;
    const card = actEl.closest<HTMLElement>(".net");
    if (!card) return;
    const act = actEl.dataset.act!;
    if (act === "cancel-pending") {
      if (!(await confirmDialog("取消加入请求", "取消等待批准？", true))) return;
      try {
        await backend.cancelPending(card.dataset.pid!);
        toast("已取消加入请求");
      } catch (err) {
        toast(String(err), "err");
      }
      await refresh();
      return;
    }
    if (act === "show-pending") {
      await showPending(card.dataset.nid!);
      return;
    }
    if (act === "members") {
      await onAction("members", card.dataset.nid!, actEl as HTMLElement);
      return;
    }
    await onAction(act, card.dataset.nid!, actEl as HTMLElement);
  });

  // Close menus on outside click
  document.addEventListener("click", (e) => {
    if (!(e.target as HTMLElement).closest('.net-menu') && !(e.target as HTMLElement).closest('.net-more')) {
      closeNetMenus();
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === 'Escape') closeNetMenus();
  });

  netList.addEventListener("change", async (e) => {
    const input = e.target as HTMLInputElement;
    if (input.dataset.act !== "toggle") return;
    const card = input.closest<HTMLElement>(".net");
    if (!card) return;
    await onToggle(card, input.checked);
  });
}

async function onToggle(card: HTMLElement, checked: boolean) {
  const nid = card.dataset.nid!;
  const input = card.querySelector<HTMLInputElement>('input[data-act="toggle"]')!;
  if (card.dataset.gone === "1") {
    input.checked = false;
    toast("该网络已从服务端删除");
    return;
  }
  input.disabled = true;
  card.classList.add("busy");
  // Flip the switch optimistically so the UI responds instantly; the real
  // state is reconciled by refresh() once the background op finishes (or
  // reverts below on failure).
  input.checked = checked;
  try {
    if (checked) await backend.rejoin(nid);
    else await backend.leave(nid);
    toast(checked ? `已连接 ${nid}` : `已断开 ${nid}`);
  } catch (e) {
    input.checked = !checked;
    toast(String(e), "err");
  } finally {
    input.disabled = false;
    card.classList.remove("busy");
    await refresh();
  }
}

let actionBusy = false;
async function onAction(act: string, nid: string, btn: HTMLButtonElement) {
  if (actionBusy) return;
  actionBusy = true;
  const origText = btn.textContent;
  btn.disabled = true;
  const setPending = (t: string) => { btn.innerHTML = `${SPIN} ${t}`; };
  try {
    // Block all ops except "delete" / "remove" for a network that has been
    // deleted from the server.
    if (act !== "delete" && act !== "remove") {
      const net = (status?.networks ?? []).find((n) => n.networkId === nid);
      if (net?.serverState === "gone") {
        toast("该网络已从服务端删除，仅可删除本地记录");
        return;
      }
    }
    switch (act) {
      case "info": {
        setPending("查询中…");
        const r = await backend.netinfo(nid);
        openModal({ title: `网络详情 · ${nid}`, body: `<pre class="json">${esc(JSON.stringify(r, null, 2))}</pre>`, wide: true });
        break;
      }
      case "members": { setPending("查询中…"); await showMembers(nid); break; }
      case "settings": { await openNetworkSettings(nid); break; }
      case "subnets": { await openSubnetRouteModal(nid); break; }
      case "invite": { setPending("生成中…"); await openInviteModal(nid); break; }
      case "code": { setPending("查询中…"); await openCodeModal(nid); break; }
      case "delete": {
        if (!(await confirmDialog("删除网络", "删除将断开所有成员并释放网段，不可恢复。确定？", true))) return;
        setPending("删除中…");
        const r = await ctlOrClean(nid, "删除网络", () => backend.deleteNet(nid));
        if (r === null) break;
        toast("网络已删除");
        break;
      }
      case "remove": {
        if (!(await confirmDialog("退出网络", "退出后需重新扫码加入。继续？", true))) return;
        setPending("退出中…");
        await backend.remove(nid);
        toast("已退出网络");
        break;
      }
    }
  } catch (e) {
    toast(String(e), "err");
  } finally {
    actionBusy = false;
    btn.disabled = false;
    btn.textContent = origText;
    await refresh();
  }
}

async function ctlOrClean<T>(nid: string, label: string, fn: () => Promise<T>): Promise<T | null> {
  try {
    return await fn();
  } catch (e: any) {
    if (String(e).includes("该网络在服务端已不存在")) {
      const ok = await confirmDialog(`${label}失败`, "该网络在服务端已不存在，是否清理本地配置？", true);
      if (ok) {
        await backend.remove(nid);
        toast("本地网络配置已清理");
        await refresh();
      }
      return null;
    }
    throw e;
  }
}

/* ── Network settings modal ───────────────────────────────────── */
async function openNetworkSettings(nid: string) {
  let detail: NetInfoDetail | undefined = ownerInfo[nid];
  if (!detail) {
    try {
      detail = await backend.netinfo(nid);
      ownerInfo[nid] = detail;
    } catch (e) {
      toast(`获取网络信息失败: ${e}`, "err");
      return;
    }
  }
  const curName = detail.name || "";
  const curSubnet = detail.subnet || "";
  openModal({
    title: `网络设置 · ${nid}`,
    body: `
      <div class="row"><label>网络名称</label><input id="s-name" type="text" value="${esc(curName)}" placeholder="留空保持不变" /></div>
      <div class="row"><label>网段</label>${subnetWidgetHTML({ id: "s-subnet", value: curSubnet, placeholder: "留空保持不变", emptyHint: "留空保持不变" })}</div>
      <div class="row"><label class="inline"><input id="s-approval" type="checkbox" ${detail.approvalRequired ? "checked" : ""} /> 新成员加入需创建者批准</label></div>
      <div class="row"><label>描述</label><textarea id="s-desc" rows="2" placeholder="简短说明这个网络的用途（分享时展示）">${esc(detail.description || "")}</textarea></div>
      <div class="row"><label>标签</label><input id="s-tags" type="text" value="${esc((detail.tags ?? []).join(", "))}" placeholder="逗号分隔，如 家庭, NAS, 异地" /></div>
      <div class="row"><label>公开范围</label>
        <select id="s-visibility">
          <option value="">私密（不公开）</option>
          <option value="shareable" ${detail.visibility === "shareable" ? "selected" : ""}>可被社区发现（shareable）</option>
        </select>
      </div>
      <p class="msg" id="s-warn" hidden>修改网段会重新分配所有成员 IP，已加入的 SNET 客户端会自动重连；但手机等外部 WireGuard 设备需手动重新导入新配置。</p>
      <p class="msg" id="s-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">保存</button>`,
    onBody: (body) => {
      const subnetWidget = subnetWidgetInit(body.querySelector<HTMLElement>(".subnet-wrap")!);
      const warn = body.querySelector<HTMLElement>("#s-warn")!;
      const subnetInput = body.querySelector<HTMLInputElement>("#s-subnet")!;
      subnetInput.addEventListener("input", () => {
        warn.hidden = subnetWidget.get() === "" || subnetWidget.get() === curSubnet;
      });
      body.querySelector("#m-submit")!.addEventListener("click", async () => {
        const name = (body.querySelector("#s-name") as HTMLInputElement).value.trim();
        const chk = subnetWidget.check();
        if (!chk.ok) {
          body.querySelector<HTMLElement>("#s-result")!.className = "msg";
          body.querySelector<HTMLElement>("#s-result")!.textContent = `网段无效：${chk.error}`;
          return;
        }
        const subnet = chk.value;
        const approval = (body.querySelector("#s-approval") as HTMLInputElement).checked;
        const desc = (body.querySelector("#s-desc") as HTMLTextAreaElement).value.trim();
        const tags = (body.querySelector("#s-tags") as HTMLInputElement).value
          .split(",").map((s) => s.trim()).filter(Boolean).slice(0, 8);
        const visibility = (body.querySelector("#s-visibility") as HTMLSelectElement).value;
        const result = body.querySelector<HTMLElement>("#s-result")!;
        const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
        const origText = submit.textContent;
        submit.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          const r = await ctlOrClean(nid, "保存设置", async () => {
            await backend.updateSettings({
              nid,
              name,
              subnet: subnet !== curSubnet ? subnet : "",
              approvalRequired: approval !== !!detail!.approvalRequired ? approval : null,
              description: desc,
              tags,
              visibility,
            });
            return true;
          });
          if (r === null) return;
          result.className = "msg ok";
          result.textContent = "已保存";
          await refreshOwnerInfo(true);
          toast("网络设置已保存");
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          submit.disabled = false;
          submit.textContent = origText;
        }
      });
    },
  });
}

/* ── Subnet route modal ───────────────────────────────────────── */
async function openSubnetRouteModal(nid: string) {
  const st = await backend.status();
  const net = (st?.networks ?? []).find((n) => n.networkId === nid);
  const curSubnets: string[] = net?.allowedSubnets ?? [];

  const renderTags = (tags: string[]) =>
    tags.length
      ? tags.map((s) => `<span class="chip">${esc(s)}<button data-del="${esc(s)}" class="chip-del" title="移除">&times;</button></span>`).join("")
      : `<span class="muted">未宣告任何子网</span>`;

  openModal({
    title: `子网路由 · ${nid}`,
    body: `
      <p class="hint">宣告本设备的局域网子网，其他成员可通过 VPN 访问。</p>
      <div id="sr-suggest" class="subnet-tags" style="margin-bottom:2px"></div>
      <div id="sr-tags" class="subnet-tags">${renderTags(curSubnets)}</div>
      <div class="subnet-add-row">
        <input type="text" id="sr-input" placeholder="如 192.168.3.0/24" />
        <button id="sr-add" class="btn ghost sm">添加</button>
      </div>
      <p class="msg" style="margin-top:8px">IP 转发将由后台服务自动开启，无需手动配置。</p>
      <p class="msg" id="sr-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="sr-save" class="btn">保存</button>`,
    onBody: (body) => {
      const tags = [...curSubnets];
      const tagsEl = body.querySelector<HTMLElement>("#sr-tags")!;
      const suggestEl = body.querySelector<HTMLElement>("#sr-suggest")!;
      const input = body.querySelector<HTMLInputElement>("#sr-input")!;
      const result = body.querySelector<HTMLElement>("#sr-result")!;

      const rerender = () => { tagsEl.innerHTML = renderTags(tags); };

      const refreshSuggest = (localSubnets: string[]) => {
        const suggestions = localSubnets.filter((s) => !tags.includes(s));
        if (!suggestions.length) { suggestEl.innerHTML = ""; return; }
        suggestEl.innerHTML =
          `<span class="muted" style="width:100%;margin-bottom:2px">检测到的本地子网</span>` +
          suggestions.map((s) => `<span class="chip" data-add="${esc(s)}" style="cursor:pointer">${esc(s)} <span style="opacity:0.5">+</span></span>`).join("");
      };

      let localSubnets: string[] = [];
      backend.detectLocalSubnets()
        .then((arr) => { localSubnets = arr; refreshSuggest(arr); })
        .catch(() => {});

      suggestEl.addEventListener("click", (e) => {
        const chip = (e.target as HTMLElement).closest<HTMLElement>("[data-add]");
        if (!chip) return;
        const cidr = chip.dataset.add!;
        if (tags.includes(cidr)) return;
        tags.push(cidr);
        rerender();
        refreshSuggest(localSubnets);
      });

      body.querySelector("#sr-add")!.addEventListener("click", () => {
        const raw = input.value.trim();
        if (!raw) return;
        if (tags.includes(raw)) { result.textContent = "该子网已添加"; return; }
        const chk = subnetCheck(raw, 24);
        if (!chk.ok) { result.className = "msg"; result.textContent = `无效: ${chk.error}`; return; }
        const cidr = chk.value;
        if (tags.includes(cidr)) { result.textContent = "该子网已添加"; return; }
        tags.push(cidr);
        input.value = "";
        result.textContent = "";
        rerender();
        refreshSuggest(localSubnets);
      });
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") { e.preventDefault(); (body.querySelector("#sr-add") as HTMLElement).click(); }
      });
      tagsEl.addEventListener("click", (e) => {
        const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-del]");
        if (!btn) return;
        const val = btn.dataset.del!;
        const idx = tags.indexOf(val);
        if (idx >= 0) tags.splice(idx, 1);
        rerender();
        refreshSuggest(localSubnets);
      });

      body.querySelector("#sr-save")!.addEventListener("click", async () => {
        const saveBtn = body.querySelector<HTMLButtonElement>("#sr-save")!;
        const origText = saveBtn.textContent;
        saveBtn.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          const r = await ctlOrClean(nid, "子网路由", async () => {
            await backend.updateSubnets({ nid, subnets: tags });
            return true;
          });
          if (r === null) return;
          result.className = "msg ok";
          result.textContent = "已保存";
          toast("子网路由已更新");
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          saveBtn.disabled = false;
          saveBtn.textContent = origText;
        }
      });
    },
  });
}

/* ── Code modal ───────────────────────────────────────────────── */
async function openCodeModal(nid: string) {
  let code = "";
  try {
    const detail = await backend.netinfo(nid);
    code = detail.pairingCode || "";
  } catch (e) {
    toast(`获取配对码失败: ${e}`, "err");
    return;
  }
  if (!code) {
    const nr = await ctlOrClean(nid, "查看配对码", () => backend.resetCode(nid));
    if (nr === null) return;
    code = nr.pairingCode;
    toast("原配对码不可用，已生成新码（旧码作废）", "warn");
  }
  openModal({
    title: `查看配对码 · ${nid}`,
    body: `
      <div class="kv"><span>配对码</span><code id="pc-code" style="letter-spacing:1.5px">${esc(code)}</code></div>
      <p class="hint" id="pc-note">配对码含有效次数与时限；复制后分享给其他设备即可加入。</p>
      <p class="msg" id="pc-result"></p>`,
    footer: `<button data-close class="btn ghost">关闭</button><button id="pc-copy" class="btn">复制配对码</button><button id="pc-reset" class="btn danger">重置配对码</button>`,
    onBody: (m) => {
      const codeEl = m.querySelector<HTMLElement>("#pc-code")!;
      const result = m.querySelector<HTMLElement>("#pc-result")!;
      const note = m.querySelector<HTMLElement>("#pc-note")!;
      const copy = m.querySelector<HTMLButtonElement>("#pc-copy")!;
      const reset = m.querySelector<HTMLButtonElement>("#pc-reset")!;
      copy.addEventListener("click", () => void copyText(code, copy));
      reset.addEventListener("click", async () => {
        if (!(await confirmDialog("重置配对码", "作废旧配对码并生成新码？旧码立即失效，已加入成员不受影响。", true))) return;
        reset.disabled = true;
        reset.innerHTML = `${SPIN} 生成中…`;
        try {
          const r = await ctlOrClean(nid, "重置配对码", () => backend.resetCode(nid));
          if (r === null) { reset.disabled = false; reset.textContent = "重置配对码"; return; }
          code = r.pairingCode;
          codeEl.textContent = code;
          result.className = "msg ok";
          result.textContent = "旧码已失效，已加入成员不受影响。";
          note.textContent = "";
          toast("已生成新配对码");
        } catch (e) {
          result.className = "msg";
          result.textContent = `重置失败: ${e}`;
        } finally {
          reset.disabled = false;
          reset.textContent = "重置配对码";
        }
        await refresh();
      });
    },
  });
}

/* ── Invite modal ─────────────────────────────────────────────── */
async function openInviteModal(nid: string) {
  let detail: NetInfoDetail;
  try {
    detail = await backend.netinfo(nid);
  } catch (e) {
    toast(`获取邀请信息失败: ${e}`, "err");
    return;
  }
  let code = detail.pairingCode;
  if (!code) {
    const nr = await ctlOrClean(nid, "邀请加入", () => backend.resetCode(nid));
    if (nr === null) return;
    code = nr.pairingCode;
    toast("原配对码不可用，已生成新码（旧码作废）", "warn");
  }
  const netId = detail.id || detail.networkId;
  if (!netId) { toast("获取网络ID失败", "err"); return; }
  const name = detail.name || netId;
  let link = `snet://join?nid=${encodeURIComponent(netId)}&code=${encodeURIComponent(code)}`;
  if (name) link += `&name=${encodeURIComponent(name)}`;
  openModal({
    title: `邀请加入 · ${name}`,
    wide: true,
    body: `
      <p class="hint">被邀请设备扫描下方二维码，或在 App 中选择「加入网络」粘贴邀请链接即可加入。</p>
      <div class="qr" id="qr"></div>
      <div class="kv"><span>邀请链接</span><code>${esc(link)}</code></div>
      <div class="kv"><span>网络ID</span><code>${esc(netId)}</code></div>
      <div class="kv"><span>配对码</span><code>${esc(code)}</code></div>
      <p class="hint">配对码含有效次数与时限，可在卡片上「查看配对码」随时查看或作废重发。</p>`,
    footer: `<button data-close class="btn ghost">关闭</button><button id="m-copy" class="btn">复制邀请链接</button>`,
    onBody: (body) => {
      renderQR(body.querySelector<HTMLElement>("#qr")!, link);
      body.querySelector("#m-copy")?.addEventListener("click", async (e) => {
        await copyText(link, e.currentTarget as HTMLButtonElement);
      });
    },
  });
}

/* ── Pending approval modal ───────────────────────────────────── */
async function showPending(nid: string) {
  try {
    const r = await backend.netinfo(nid);
    ownerInfo[nid] = r;
    const pending = r.pending ?? [];
    if (!pending.length) {
      openModal({ title: `待批准请求 · ${nid}`, body: `<p class="muted">暂无待批准请求</p>` });
      return;
    }
    const rows = pending.map((p) => {
      return `<tr>
        <td><code>${p.deviceId ? esc(p.deviceId.slice(0, 12)) + "…" : "-"}</code></td>
        <td><code>${esc(p.publicKey.slice(0, 12))}…</code></td>
        <td><span class="pill warn">待批准</span></td>
        <td>
          <button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button>
          <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button>
        </td>
      </tr>`;
    }).join("");
    openModal({
      title: `待批准请求 · ${nid}`,
      body: `<table class="members"><thead><tr><th>设备</th><th>公钥</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table>`,
    });
    const modal = $("#modal-body");
    if (modal) {
      modal.addEventListener("click", async (e) => {
        const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-pend]");
        if (!btn) return;
        const pid = btn.dataset.pend!;
        const isDeny = btn.classList.contains("danger");
        if (isDeny) {
          if (!(await confirmDialog("拒绝加入", "确定拒绝该设备的加入请求？", true))) return;
        }
        try {
          if (isDeny) await backend.deny(nid, pid);
          else await backend.approve(nid, pid);
          toast(isDeny ? "已拒绝" : "已批准");
          await refresh();
          await showPending(nid);
        } catch (err) {
          toast(String(err), "err");
        }
      });
    }
  } catch (e) {
    toast(String(e), "err");
  }
}

/* ── Members modal ────────────────────────────────────────────── */
function roleLabel(role: string): string {
  if (role === "owner") return "创建者";
  if (role === "admin") return "管理员";
  return "成员";
}

async function showMembers(nid: string) {
  const mine = (status?.networks ?? []).find((n) => n.networkId === nid);
  if (mine?.serverState === "gone") {
    toast("该网络已删除，无法查看成员");
    return;
  }
  const myIp = mine?.ip;
  if (mine?.owner) {
    const r = await backend.netinfo(nid);
    ownerInfo[nid] = r;
    const rows = (r.nodes ?? []).map((nd) => {
      const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
      const isSelf = nd.ip === myIp || nd.deviceId === (status?.deviceId ?? "");
      const kick = nd.ip === myIp ? `<span class="muted">自己</span>` : `<button data-node="${esc(nd.id)}" class="btn danger ghost sm">踢出</button>`;
      const role = nd.role || "member";
      const roleCtl = isSelf || role === "owner"
        ? `<span class="pill role-${esc(role)}">${roleLabel(role)}</span>`
        : `<select data-role="${esc(nd.id)}" data-cur="${esc(role)}" class="role-select">
             <option value="member" ${role === "member" ? "selected" : ""}>成员</option>
             <option value="admin" ${role === "admin" ? "selected" : ""}>管理员</option>
           </select>`;
      const subnets = (nd.allowedSubnets?.length ?? 0) > 0
        ? `<span class="pill subnet-route" title="子网路由：${esc(nd.allowedSubnets!.join(", "))}">${esc(nd.allowedSubnets!.join(", "))}</span>`
        : `<span class="muted">-</span>`;
      return `<tr><td class="mono">${esc(nd.ip)}</td><td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td><td>${online}</td><td>${roleCtl}</td><td>${subnets}</td><td>${kick}</td></tr>`;
    }).join("");
    const pendingRows = (r.pending ?? []).map((p) => {
      return `<tr><td colspan="2"><span class="muted">设备</span> <code>${p.deviceId ? esc(p.deviceId.slice(0, 8)) + "…" : "-"}</code><span class="muted"> 公钥</span> <code>${esc(p.publicKey.slice(0, 12))}…</code></td><td><span class="pill warn">待批准</span></td><td>-</td><td><button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button> <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button></td></tr>`;
    }).join("");
    const pendingSection = pendingRows
      ? `<div class="pending-block"><h4>待批准加入请求</h4><div class="tbl-wrap"><table class="members"><tbody>${pendingRows}</tbody></table></div></div>`
      : "";
    openModal({
      title: `成员 · ${nid}`,
      wide: true,
      body: `<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>角色</th><th>子网路由</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>${pendingSection}`,
      onBody: (b) => {
        b.querySelectorAll<HTMLElement>("[data-node]").forEach((k) =>
          k.addEventListener("click", async () => {
            if (!(await confirmDialog("踢出成员", "踢出该成员？该设备将立即断开。", true))) return;
            const btn = k as HTMLButtonElement;
            const orig = btn.textContent;
            btn.disabled = true;
            btn.textContent = "踢出中…";
            try {
              const r = await ctlOrClean(nid, "踢出成员", () => backend.kick({ nid, nodeId: k.dataset.node! }));
              if (r === null) return;
              toast("已踢出");
              closeModal();
            } catch (e) {
              toast(String(e), "err");
              btn.disabled = false;
              btn.textContent = orig;
            }
            await refresh();
          }),
        );
        b.querySelectorAll<HTMLSelectElement>("[data-role]").forEach((sel) =>
          sel.addEventListener("change", async () => {
            if (!(await confirmDialog("修改角色", `将该成员设为「${roleLabel(sel.value as string)}」？`, true))) {
              sel.value = sel.dataset.cur!;
              return;
            }
            const origVal = sel.value;
            sel.disabled = true;
            try {
              const r = await ctlOrClean(nid, "修改角色", () =>
                backend.setRole!({ nid, nodeId: sel.dataset.role!, role: sel.value }));
              if (r === null) { sel.disabled = false; sel.value = sel.dataset.cur!; return; }
              sel.dataset.cur = origVal;
              toast("角色已更新");
            } catch (e) {
              toast(String(e), "err");
              sel.value = sel.dataset.cur!;
            }
            sel.disabled = false;
            await refresh();
          }),
        );
        b.querySelectorAll<HTMLElement>("[data-pend]").forEach((k) => {
          const deny = k.classList.contains("danger");
          k.addEventListener("click", async () => {
            const pid = k.dataset.pend!;
            if (!(await confirmDialog(deny ? "拒绝加入请求" : "批准加入请求", deny ? "拒绝后该设备无法加入。继续？" : "批准后该设备立即加入网络。继续？", deny))) return;
            const btn = k as HTMLButtonElement;
            const orig = btn.textContent;
            btn.disabled = true;
            btn.textContent = deny ? "拒绝中…" : "批准中…";
            try {
              if (deny) await backend.deny({ nid, pendingId: pid });
              else await backend.approve({ nid, pendingId: pid });
              toast(deny ? "已拒绝" : "已批准");
              closeModal();
            } catch (e) {
              toast(String(e), "err");
              btn.disabled = false;
              btn.textContent = orig;
            }
            await refresh();
          });
        });
      },
    });
    return;
  }
  // Non-owner: read-only peers view
  const r = await backend.peers(nid);
  const all = r.self ? [r.self, ...(r.peers ?? [])] : (r.peers ?? []);
  const rows = all.map((nd) => {
    const isSelf = nd.ip === myIp;
    const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
    const subnets = (nd.allowedSubnets?.length ?? 0) > 0
        ? `<span class="pill subnet-route" title="子网路由：${esc(nd.allowedSubnets!.join(", "))}">${esc(nd.allowedSubnets!.join(", "))}</span>`
        : `<span class="muted">-</span>`;
    return `<tr><td class="mono">${esc(nd.ip)}</td><td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td><td>${online}</td><td>${subnets}</td><td>${isSelf ? `<span class="muted">自己</span>` : ""}</td></tr>`;
  }).join("");
  openModal({
    title: `成员 · ${nid}`,
    wide: true,
    body: `<p class="hint">成员列表（只读，本机非创建者）</p><div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>子网路由</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`,
  });
}

/* ── Create modal ─────────────────────────────────────────────── */
function openCreateModal() {
  const s = loadSettings();
  const hasOwner = (status?.networks ?? []).some((n) => n.owner);
  const srv = currentServer();
  const hint = hasOwner
    ? `<p class="msg">本设备已创建网络（每客户端仅能创建一个）。如需新网络，请先删除或退出当前网络。</p>`
    : "";
  const serverHint = srv
    ? `<p class="hint">将在已连接的服务器上创建：<code>${esc(srv)}</code></p>`
    : `<p class="msg">未连接服务器：请先在「设置」中链接服务器。</p>`;
  openModal({
    title: "创建网络",
    body: `${hint}
      <div class="row"><label>网络名称</label><input id="m-name" type="text" placeholder="例如：家庭网络" /></div>
      <div class="row"><label>网段</label>${subnetWidgetHTML({ id: "m-subnet", placeholder: "留空自动分配（如 10.88.0.0/24）", emptyHint: "留空自动分配，通常为 10.88.N.0/24" })}</div>
      <div class="row"><label>描述（可选）</label><textarea id="m-desc" rows="2" placeholder="简短说明这个网络的用途"></textarea></div>
      <div class="row"><label>标签（可选）</label><input id="m-tags" type="text" placeholder="逗号分隔，如 家庭, NAS" /></div>
      ${serverHint}
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn" ${hasOwner || !srv ? "disabled" : ""}>创建</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const subnetWidget = subnetWidgetInit(body.querySelector<HTMLElement>(".subnet-wrap")!);
      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = `${SPIN} 创建中…`;
        result.className = "msg";
        result.textContent = "";
        try {
          const name = (body.querySelector("#m-name") as HTMLInputElement).value.trim();
          const chk = subnetWidget.check();
          if (!chk.ok) throw new Error(`网段无效：${chk.error}`);
          const subnet = chk.value;
          const description = (body.querySelector("#m-desc") as HTMLTextAreaElement).value.trim();
          const tags = (body.querySelector("#m-tags") as HTMLInputElement).value
            .split(",").map((s) => s.trim()).filter(Boolean).slice(0, 8);
          const server = currentServer();
          if (!name) throw new Error("请输入网络名称");
          if (!server) throw new Error("未连接服务器：请先在设置中链接服务器");
          const r: CreateResp = await backend.create({ server, port: s.wgport, ca: s.ca, name, subnet, approvalRequired: false, description, tags, visibility: "" });
          const modalEl = body;
          const mBody = modalEl.querySelector<HTMLElement>(".modal-body")!;
          const mFoot = modalEl.querySelector<HTMLElement>(".modal-foot")!;
          mBody.innerHTML = `
            <div class="create-ok">
              <p class="msg ok">网络创建成功，邀请其他设备加入：</p>
              <div class="qr" id="qr"></div>
              <div class="kv"><span>网络ID</span><code>${esc(r.networkId)}</code></div>
              <div class="kv"><span>配对码</span><code>${esc(r.pairingCode)}</code></div>
              <div class="kv"><span>邀请链接</span><code>${esc(r.link)}</code></div>
            </div>`;
          renderQR(mBody.querySelector<HTMLElement>("#qr")!, r.link);
          mFoot.innerHTML = `<button id="m-copy" class="btn">复制邀请链接</button><button id="m-close" class="btn ghost">关闭</button>`;
          mFoot.querySelector<HTMLButtonElement>("#m-copy")!.addEventListener("click", async (e) => {
            await copyText(r.link, e.currentTarget as HTMLButtonElement);
          });
          mFoot.querySelector<HTMLButtonElement>("#m-close")!.addEventListener("click", closeModal);
          await refresh();
        } catch (e) {
          result.className = "msg";
          result.textContent = `创建失败: ${e}`;
          submit.disabled = false;
          submit.innerHTML = "创建";
        }
      });
    },
  });
}

/* ── Join modal ───────────────────────────────────────────────── */
function normalizeServerCompare(s: string): string {
  return s.replace(/\/+$/, "");
}
function linkServerOf(link: string): string | undefined {
  try {
    const u = new URL(link);
    if (u.protocol === "snet:" || u.protocol === "http:" || u.protocol === "https:") {
      return u.searchParams.get("server") || undefined;
    }
  } catch { /* not a URL */ }
  return undefined;
}

function openJoinModal() {
  const s = loadSettings();
  const srv = currentServer();
  const serverHint = srv
    ? `<p class="hint">将加入已连接服务器上的网络：<code>${esc(srv)}</code></p>`
    : `<p class="msg">未连接服务器：请先在「设置」中链接服务器；或粘贴带有服务器地址的邀请链接后加入。</p>`;
  openModal({
    title: "加入网络",
    body: `<div class="row"><label>邀请链接</label>
      <div class="subnet-input join-link-row">
        <input class="join-link-input" id="m-link" type="text" placeholder="snet://join?nid=...&code=..." />
        ${backend.scanQR ? '<button id="m-scan" class="btn ghost" type="button" title="扫描二维码加入">扫码</button>' : ""}
      </div></div>
      <p class="hint" style="text-align:center">或手动输入</p>
      <div class="row"><label>网络ID</label><input id="m-nid" type="text" placeholder="6 位网络ID" /></div>
      <div class="row"><label>配对码</label><input id="m-code" type="text" placeholder="12 位配对码" /></div>
      ${serverHint}
      <div id="m-bind-auth" hidden></div>
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">加入</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const authWrap = body.querySelector<HTMLElement>("#m-bind-auth")!;

      const linkInput = body.querySelector<HTMLInputElement>("#m-link")!;
      const scanBtn = body.querySelector<HTMLButtonElement>("#m-scan");
      scanBtn?.addEventListener("click", async () => {
        if (!backend.scanQR) return;
        scanBtn.disabled = true;
        scanBtn.textContent = "扫描中…";
        try {
          const text = (await backend.scanQR()).trim();
          if (!/^snet:\/\/join\?/.test(text)) {
            toast("不是有效的加入邀请链接", "err");
            return;
          }
          linkInput.value = text;
          toast("已读取邀请链接");
          submit.focus();
        } catch (e) {
          toast(String(e), "err");
        } finally {
          scanBtn.disabled = false;
          scanBtn.textContent = "扫码";
        }
      });

      const bindAndThen = (server: string, then: () => Promise<void>) => {
        authWrap.hidden = false;
        authWrap.innerHTML = `
          <div class="settings-block">
            <p class="msg">该邀请属于服务器 <code>${esc(server)}</code>，本机尚未绑定该服务器。请输入设备授权码以绑定后加入：</p>
            <div class="row"><label>设备授权码</label><input id="m-bind-code" type="text" placeholder="管理端生成的授权码" autocomplete="off" /></div>
            <div class="settings-actions"><button id="m-bind-go" class="btn">绑定并加入</button></div>
            <p class="msg" id="m-bind-result"></p>
          </div>`;
        const codeInput = authWrap.querySelector<HTMLInputElement>("#m-bind-code")!;
        const bindResult = authWrap.querySelector<HTMLElement>("#m-bind-result")!;
        const bindBtn = authWrap.querySelector<HTMLButtonElement>("#m-bind-go")!;
        codeInput.focus();
        const go = async () => {
          const code = codeInput.value.trim();
          if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
          bindBtn.disabled = true;
          bindResult.className = "msg"; bindResult.textContent = "正在绑定…";
          try {
            const ca = s.ca;
            await backend.bind({ server, ca, code });
            saveSettings({ ...loadSettings(), server, ca });
            bindResult.className = "msg ok"; bindResult.textContent = `已绑定 ${server}，正在加入…`;
            await then();
          } catch (e) {
            bindResult.className = "msg"; bindResult.textContent = `绑定失败: ${e}`;
            bindBtn.disabled = false;
          }
        };
        bindBtn.addEventListener("click", go);
        codeInput.addEventListener("keydown", (e) => { if (e.key === "Enter") void go(); });
      };

      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = `${SPIN} 加入中…`;
        result.className = "msg";
        result.textContent = "";
        try {
          const ca = s.ca;
          let link = (body.querySelector("#m-link") as HTMLInputElement).value.trim();
          if (!link) {
            const nid = (body.querySelector("#m-nid") as HTMLInputElement).value.trim();
            const code = (body.querySelector("#m-code") as HTMLInputElement).value.trim();
            if (!nid || !code) throw new Error("请输入邀请链接，或网络ID + 配对码");
            link = `snet://join?nid=${nid}&code=${code}`;
          }
          const linkServer = linkServerOf(link);
          const srv = currentServer();

          class NeedBind extends Error {}
          const doJoin = async () => {
            const server = linkServer && linkServer.trim() ? linkServer : srv;
            if (!server) throw new Error("未连接服务器：请先在设置中链接服务器");
            try {
              const r: JoinResp = await backend.join({ server, port: s.wgport, ca, link });
              result.className = "msg ok";
              if (r.status === "pending") {
                result.innerHTML = `<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>`;
              } else {
                result.textContent = `已加入: IP ${r.ip ?? "-"}，网络 ${r.networkId ?? "-"}`;
              }
              skipOnboarding = true;
              await refresh();
              skipOnboarding = false;
              closeModal();
            } catch (e) {
              if (String(e).includes("设备未授权")) throw new NeedBind();
              throw e;
            }
          };

          if (linkServer && srv && normalizeServerCompare(linkServer) === normalizeServerCompare(srv)) {
            await doJoin();
          } else if (linkServer) {
            try {
              await doJoin();
            } catch (e) {
              if (e instanceof NeedBind) {
                await new Promise<void>((resolve, reject) => {
                  bindAndThen(linkServer, async () => {
                    try { await doJoin(); resolve(); }
                    catch (e2) { result.className = "msg"; result.textContent = `加入失败: ${e2}`; submit.disabled = false; reject(e2); }
                  });
                });
              } else { throw e; }
            }
          } else {
            await doJoin();
          }
        } catch (e) {
          result.className = "msg";
          result.textContent = `加入失败: ${e}`;
          submit.disabled = false;
          submit.innerHTML = "加入";
        }
      });
    },
  });
}

/* ── App settings modal ───────────────────────────────────────── */
const HELP_ROWS: Array<[string, string, string]> = [
  ["链接开关", "全部", "开启＝连接该网络隧道；关闭＝断开本机该网络，保留配置与服务器节点，可随时再开"],
  ["详情", "创建者", "查看服务器上该网络的完整信息（成员、中继端口、在线状态、创建时间等）"],
  ["成员", "创建者", "查看成员列表与在线状态；批准/拒绝待批准加入请求、踢出成员"],
  ["设置", "创建者", "修改网络名称、网段，或开启「新成员需批准」"],
  ["查看配对码", "创建者", "查看当前配对码并复制；可作废旧码并生成新码，旧码立即失效、已加入成员不受影响"],
  ["删除", "创建者", "彻底删除该网络：所有成员断开、网段释放，不可恢复"],
  ["退出网络", "成员", "本机移出该网络并遗忘配置，需重新扫码加入；创建者无此按钮"],
];

function openSettingsModal() {
  const s = loadSettings();
  const helpRows = HELP_ROWS.map(([op, who, desc]) => `<tr><td>${op}</td><td>${who}</td><td>${desc}</td></tr>`).join("");
  openModal({
    title: "设置",
    body: `<div class="settings-block">
        <div class="row"><label>服务器地址</label><input id="s-server" type="text" value="${esc(s.server)}" placeholder="https://example.com:8090" /></div>
        <div class="row"><label>设备授权码</label><input id="s-code" type="text" placeholder="管理端生成的设备授权码（仅用于链接，不保存）" autocomplete="off" /></div>
        <div class="row"><label>CA 证书路径</label><input id="s-ca" type="text" value="${esc(s.ca)}" placeholder="公共证书(如 Let's Encrypt)留空；自签名服务器填证书路径" /></div>
        <div class="settings-actions">
          <button id="s-bind" class="btn">链接服务器</button>
        </div>
        <p class="msg" id="s-bind-result"></p>
      </div>
      <div class="row"><label>WireGuard 端口</label><input id="s-wgport" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="settings-actions" id="s-daemon-row">
        ${backend.hasDaemonControl
          ? (status ? `<span class="live-dot ok">运行中</span>` : `<button id="s-start-daemon" class="btn ghost">启动</button>`)
          : ""}
      </div>
      <p class="msg" id="s-msg"></p>
      <details class="help">
        <summary>操作说明</summary>
        <div class="tbl-wrap">
        <table class="help">
          <thead><tr><th>操作</th><th>适用</th><th>作用</th></tr></thead>
          <tbody>${helpRows}</tbody>
        </table>
        </div>
      </details>`,
    wide: true,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const msg = body.querySelector<HTMLElement>("#s-msg")!;
      const bindResult = body.querySelector<HTMLElement>("#s-bind-result")!;
      const bindBtn = body.querySelector<HTMLButtonElement>("#s-bind");

      body.querySelector("#s-bind")?.addEventListener("click", async () => {
        const server = (body.querySelector("#s-server") as HTMLInputElement).value.trim();
        const ca = (body.querySelector("#s-ca") as HTMLInputElement).value.trim();
        const code = (body.querySelector("#s-code") as HTMLInputElement).value.trim();
        if (!server) { bindResult.className = "msg"; bindResult.textContent = "请输入服务器地址"; return; }
        if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
        if (bindBtn) {
          bindBtn.disabled = true;
          bindBtn.innerHTML = `${SPIN} 正在链接…`;
        }
        bindResult.className = "msg"; bindResult.textContent = "正在链接服务器，请稍候…";
        try {
          await backend.bind({ server, ca, code });
          saveSettings({ ...loadSettings(), server, ca });
          bindResult.className = "msg ok"; bindResult.textContent = `已绑定 ${server}`;
          toast("已绑定服务器");
          skipOnboarding = true;
          await refresh();
          skipOnboarding = false;
          closeModal();
        } catch (e) {
          bindResult.className = "msg"; bindResult.textContent = `链接失败: ${e}`;
          if (bindBtn) {
            bindBtn.disabled = false;
            bindBtn.textContent = "链接服务器";
          }
        }
      });

      body.querySelector("#s-start-daemon")?.addEventListener("click", async () => {
        msg.className = "msg";
        msg.textContent = "正在启动…";
        try {
          await backend.ensureDaemon();
          msg.className = "msg ok";
          msg.textContent = "后台服务已就绪";
          await refresh();
          const row = body.querySelector("#s-daemon-row");
          if (row) row.innerHTML = '<span class="live-dot ok">后台服务: 运行中</span>';
        } catch (e) {
          msg.className = "msg"; msg.textContent = String(e);
        }
      });
    },
  });
}

/* ── Onboarding ───────────────────────────────────────────────── */
function maybeShowOnboarding() {
  if (skipOnboarding) return;
  if (onboardingShown) return;
  if (status && status.bound) return;
  if (status && (status.networks?.length || status.pendingJoins?.length)) return;
  onboardingShown = true;
  renderOnboarding();
}

function renderOnboarding() {
  const ov = $("#onboarding");
  if (!ov) return;
  const s = loadSettings();
  const serverInput = $("#onb-server") as HTMLInputElement;
  const caInput = $("#onb-ca") as HTMLInputElement;
  if (serverInput) serverInput.value = s.server;
  if (caInput) caInput.value = s.ca;
  const daemonSection = $("#onb-daemon");
  if (daemonSection) daemonSection.hidden = !!status;

  const startDaemon = $("#onb-start-daemon") as HTMLButtonElement;
  if (startDaemon) {
    startDaemon.onclick = async () => {
      startDaemon.disabled = true;
      startDaemon.textContent = "启动中…";
      const daemonResult = $("#onb-daemon-result") as HTMLElement;
      daemonResult.className = "msg";
      daemonResult.textContent = "正在启动…";
      try {
        await backend.ensureDaemon();
        daemonResult.className = "msg ok";
        daemonResult.textContent = "后台服务已就绪";
        await refresh();
        if (daemonSection) daemonSection.hidden = true;
      } catch (e) {
        daemonResult.className = "msg";
        daemonResult.textContent = `启动失败: ${e}`;
      } finally {
        startDaemon.disabled = false;
        startDaemon.textContent = "启动";
      }
    };
  }

  const bindResult = $("#onb-bind-result") as HTMLElement;
  ($("#onb-bind") as HTMLButtonElement).onclick = async () => {
    const server = ($("#onb-server") as HTMLInputElement).value.trim();
    const ca = ($("#onb-ca") as HTMLInputElement).value.trim();
    const code = ($("#onb-code") as HTMLInputElement).value.trim();
    if (!server) { bindResult.className = "msg"; bindResult.textContent = "请输入服务器地址"; return; }
    if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
    bindResult.className = "msg"; bindResult.textContent = "正在绑定…";
    try {
      await backend.bind({ server, ca, code });
      saveSettings({ ...loadSettings(), server, ca });
      bindResult.className = "msg ok"; bindResult.textContent = `已绑定 ${server}`;
      toast("已绑定服务器");
      await finishOnboarding();
    } catch (e) {
      bindResult.className = "msg"; bindResult.textContent = `绑定失败: ${e}`;
    }
  };

  ($("#onb-skip") as HTMLAnchorElement).onclick = (e) => {
    e.preventDefault();
    finishOnboarding();
  };

  ov.hidden = false;
}

async function finishOnboarding() {
  const ov = $("#onboarding");
  if (ov) ov.hidden = true;
  await refresh();
}

/* ── Status tab ───────────────────────────────────────────────── */
function renderStatus() {
  const deviceId = $("#device-id");
  const svcServer = $("#svc-server");
  const svcWgport = $("#svc-wgport");
  const tunnelDetail = $("#tunnel-detail");
  const statusJson = $("#status-json");
  if (!status) {
    if (deviceId) deviceId.textContent = "-";
    if (svcServer) svcServer.textContent = "-";
    if (svcWgport) svcWgport.textContent = "-";
    const emptyHtml = `<p class="muted">后台服务未运行</p>`;
    if (tunnelDetail && emptyHtml !== lastTunnelHTML) {
      tunnelDetail.innerHTML = emptyHtml;
      lastTunnelHTML = emptyHtml;
    }
    if (statusJson) statusJson.textContent = "(未连接后台服务)";
    return;
  }
  if (deviceId) deviceId.textContent = status.deviceId || "-";
  const addr = status.serverAddr ?? "";
  if (svcServer) svcServer.textContent = status.bound && addr ? addr : "未连接服务器";
  if (svcWgport) svcWgport.textContent = String(status.wgPort ?? "-");
  const nets = status.networks ?? [];
  if (tunnelDetail) {
    const html = nets.length
      ? nets.map((n) => {
          const stats = Object.values(n.peerStats ?? {});
          const bytes = stats.reduce((a, p) => a + (p.RxBytes ?? 0) + (p.TxBytes ?? 0), 0);
          const peers = stats.length;
          const linked = !!n.interface;
          return `<div class="kv"><span>${esc(n.name || n.networkId)}</span>
            <code>${esc(n.interface || "无隧道")}</code>
            <span class="muted">收/发 ${fmtBytes(bytes)} · 成员 ${peers ? `${onlineCount(n)}/${peers} 在线` : "暂无其他成员"}</span>
            ${linked ? '<span class="pill ok">已链接</span>' : '<span class="pill off">未链接</span>'}
          </div>`;
        }).join("")
      : `<p class="muted">未加入任何网络</p>`;
    if (html !== lastTunnelHTML) {
      tunnelDetail.innerHTML = html;
      lastTunnelHTML = html;
    }
  }
  if (statusJson) statusJson.textContent = JSON.stringify(status, null, 2);
}

/* ── Ensure daemon ────────────────────────────────────────────── */
async function ensureDaemon() {
  const btn = $("#header-svc-btn") as HTMLButtonElement;
  if (btn) { btn.disabled = true; btn.textContent = "启动中…"; }
  try {
    await backend.ensureDaemon();
    toast("后台服务已就绪");
  } catch (e) {
    toast(String(e), "err");
  } finally {
    if (btn) btn.disabled = false;
    await refresh();
  }
}

/* ── Event bindings ───────────────────────────────────────────── */
function bindEvents() {
  $("#btn-settings")?.addEventListener("click", openSettingsModal);
  $("#btn-create")?.addEventListener("click", openCreateModal);
  $("#btn-join")?.addEventListener("click", openJoinModal);
  $("#btn-refresh")?.addEventListener("click", async () => { await refresh(); toast("已刷新"); });
  $("#btn-leave-all")?.addEventListener("click", async () => {
    try {
      await backend.leave("");
      toast("已停止全部网络");
    } catch (e) {
      toast(String(e), "err");
    }
    await refresh();
  });

  // Mobile header dropdown
  const btnMore = $("#btn-more") as HTMLButtonElement | null;
  const dropdown = $("#header-dropdown") as HTMLElement | null;
  if (btnMore && dropdown) {
    btnMore.addEventListener("click", (e) => {
      e.stopPropagation();
      dropdown.hidden = !dropdown.hidden;
    });
    document.addEventListener("click", () => { dropdown.hidden = true; });
    dropdown.querySelectorAll<HTMLElement>("[data-action]").forEach((item) => {
      item.addEventListener("click", () => {
        dropdown.hidden = true;
        const action = item.dataset.action;
        if (action === "refresh") void refresh();
        else if (action === "settings") openSettingsModal();
        else if (action === "logout") { /* logout handled in adapter */ window.dispatchEvent(new Event("snet-logout")); }
      });
    });
  }
}

/* ── Init ─────────────────────────────────────────────────────── */
export async function init(b: Backend) {
  backend = b;
  await loadQR();
  initTabs();
  bindNetListEvents();
  bindEvents();
  // Hide logout button on Desktop/Android (not needed without login)
  if (backend.hasDaemonControl) {
    const lo = $("#btn-logout") as HTMLElement | null;
    if (lo) lo.style.display = "none";
    document.querySelectorAll<HTMLElement>("[data-action=\"logout\"]").forEach((el) => { el.style.display = "none"; });
  }
  refresh();
  setInterval(() => {
    if (!document.hidden && !refreshing) refresh();
  }, 5000);
}
