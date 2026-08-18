import { invoke } from "@tauri-apps/api/core";
import qrcode from "qrcode-generator";

type PeerStats = { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number };
type NetInfo = {
  networkId: string;
  name?: string;
  ip?: string;
  subnet?: string;
  port?: number;
  active?: boolean;
  owner?: boolean;
  error?: string;
  interface?: string;
  peerStats?: Record<string, PeerStats>;
};
type PendingJoin = {
  pendingId: string;
  networkId: string;
  createdAt?: string;
  status?: string;
  error?: string;
};
type DaemonStatus = {
  deviceId?: string;
  serverAddr?: string;
  bound?: boolean;
  wgPort?: number;
  networks?: NetInfo[];
  pendingJoins?: PendingJoin[];
};
type NetInfoDetail = {
  id?: string;
  networkId?: string;
  name?: string;
  subnet?: string;
  approvalRequired?: boolean;
  pendingCount?: number;
  nodeCount?: number;
  pairingCode?: string;
  pending?: Array<{ id: string; publicKey: string; deviceId?: string; createdAt?: string }>;
  nodes?: Array<{ id: string; ip: string; deviceId?: string; online?: boolean }>;
};
type NetNode = { id: string; ip: string; publicKey?: string; deviceId?: string; online?: boolean };
type PeersResp = { peers?: NetNode[]; self?: NetNode; subnet?: string; approvalRequired?: boolean };

const call = <T>(cmd: string, args?: Record<string, unknown>): Promise<T> => invoke<T>(cmd, args);

const $ = <T extends HTMLElement>(sel: string): T => document.querySelector<T>(sel)!;

const esc = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

const SPIN = '<span class="spinner"></span>';

/* ── 二维码 ───────────────────────────────────── */
function renderQR(container: HTMLElement, text: string) {
  container.innerHTML = "";
  const qr = qrcode(0, "M");
  qr.addData(text);
  qr.make();
  const svg = qr.createSvgTag({ cellSize: 4, margin: 1, scalable: true });
  container.innerHTML = svg;
  container.querySelector("svg")?.setAttribute("style", "width:100%;height:auto;display:block");
}

/* ── 网段输入控件 ───────────────────────────────────
 * 规则镜像服务端 internal/server/store.go validateSubnet：
 * 仅 IPv4 私有网段（10/8、172.16/12、192.168/16），前缀 /8–/24，
 * 必须是网络地址（主机位清零）。空值=自动分配（创建）/不变（编辑）。
 * 修改此文件校验时须同步 internal/server/admin.html 中的同名函数。
 */
type SubnetCheck =
  | { ok: true; value: string; fix?: boolean; text: string }
  | { ok: false; error: string };

const SUBNET_PREFIXES = [24, 23, 22, 20, 16, 12, 8];
const SUBNET_CHIPS = ["10.88.0.0/24", "10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"];

function subnetIsPrivate(u32: number): boolean {
  if ((u32 >>> 24) === 10) return true; // 10/8
  if (u32 >= 0xac100000 && u32 <= 0xac1fffff) return true; // 172.16/12
  if (u32 >= 0xc0a80000 && u32 <= 0xc0a8ffff) return true; // 192.168/16
  return false;
}

function subnetBaseU32(u32: number, prefix: number): number {
  const mask = (~0 << (32 - prefix)) >>> 0;
  return (u32 & mask) >>> 0;
}

function u32ToIp(u32: number): string {
  return `${u32 >>> 24}.${(u32 >>> 16) & 255}.${(u32 >>> 8) & 255}.${u32 & 255}`;
}

// 解析 "10.88.0.0" 或 "10.88.0.0/24"；返回 32 位地址与显式前缀（无前缀用 selectPrefix）。
function subnetCheck(raw: string, selectPrefix: number): SubnetCheck {
  const s = raw.trim();
  if (!s) return { ok: true, value: "", text: "" };
  let host = s;
  let prefix = selectPrefix;
  const slash = s.indexOf("/");
  if (slash >= 0) {
    host = s.slice(0, slash);
    const p = Number(s.slice(slash + 1).trim());
    if (!Number.isInteger(p) || p < 8 || p > 24) {
      return { ok: false, error: "前缀需为 /8–/24" };
    }
    prefix = p;
  }
  const parts = host.split(".");
  if (parts.length !== 4) return { ok: false, error: "格式无效，如 10.88.0.0/24" };
  const oct = parts.map((o) => {
    if (!/^\d{1,3}$/.test(o)) return NaN;
    return Number(o);
  });
  if (oct.some((o) => Number.isNaN(o) || o < 0 || o > 255)) {
    return { ok: false, error: "IPv4 地址段需在 0–255" };
  }
  const u32 = ((oct[0] << 24) | (oct[1] << 16) | (oct[2] << 8) | oct[3]) >>> 0;
  const base = subnetBaseU32(u32, prefix);
  if (!subnetIsPrivate(base)) {
    return { ok: false, error: "仅支持私有内网段：10/8、172.16/12、192.168/16" };
  }
  const value = `${u32ToIp(base)}/${prefix}`;
  if (base !== u32) {
    return { ok: true, value, fix: true, text: `主机位应为 0，已修正为 ${value}` };
  }
  const capacity = Math.max(0, 2 ** (32 - prefix) - 2).toLocaleString("en-US");
  return { ok: true, value, text: `✓ ${value} · 私有网段，可容纳 ${capacity} 台` };
}

type SubnetWidget = {
  check: () => SubnetCheck;
  get: () => string; // 规范化 CIDR；空值返回 ""
  render: () => void;
};

function subnetWidgetHTML(opts: { id?: string; value?: string; placeholder?: string; emptyHint?: string }): string {
  const id = opts.id || "";
  const value = opts.value || "";
  let base = "";
  let prefix = 24;
  if (value) {
    const slash = value.indexOf("/");
    base = slash >= 0 ? value.slice(0, slash) : value;
    const p = Number(slash >= 0 ? value.slice(slash + 1) : "");
    if (Number.isInteger(p) && p >= 8 && p <= 24) prefix = p;
  }
  const prefixOpts = SUBNET_PREFIXES.map((p) => `<option value="${p}" ${p === prefix ? "selected" : ""}>/${p}</option>`).join("");
  const chips = SUBNET_CHIPS.map((c) => `<button type="button" class="chip" data-subnet="${esc(c)}">${esc(c)}</button>`).join("");
  const empty = opts.emptyHint || opts.placeholder || "";
  return `
    <div class="subnet-wrap" data-empty="${esc(empty)}">
      <div class="subnet-input">
        <input id="${id}" class="subnet-base" type="text" value="${esc(base)}" placeholder="${esc(opts.placeholder || "10.88.0.0/24（留空自动分配）")}" spellcheck="false" autocomplete="off" />
        <select class="subnet-prefix" aria-label="前缀">${prefixOpts}</select>
      </div>
      <div class="subnet-chips">${chips}</div>
      <p class="subnet-status"></p>
    </div>`;
}

function subnetWidgetInit(wrap: HTMLElement): SubnetWidget {
  const input = wrap.querySelector<HTMLInputElement>(".subnet-base")!;
  const sel = wrap.querySelector<HTMLSelectElement>(".subnet-prefix")!;
  const status = wrap.querySelector<HTMLElement>(".subnet-status")!;
  const emptyHint = wrap.dataset.empty || "";

  const render = () => {
    const s = input.value.trim();
    if (!s) {
      status.className = "subnet-status";
      status.textContent = emptyHint;
      return;
    }
    const chk = subnetCheck(s, Number(sel.value));
    if (!chk.ok) {
      status.className = "subnet-status err";
      status.textContent = chk.error;
      return;
    }
    status.className = chk.fix ? "subnet-status warn" : "subnet-status ok";
    status.textContent = chk.text;
  };

  input.addEventListener("input", () => {
    const m = input.value.match(/\/(\d{1,2})\s*$/);
    if (m) {
      const p = Number(m[1]);
      if (Array.from(sel.options).some((o) => Number(o.value) === p)) sel.value = String(p);
    }
    render();
  });

  sel.addEventListener("change", () => {
    const s = input.value.trim();
    const slash = s.indexOf("/");
    const base = slash >= 0 ? s.slice(0, slash) : s;
    if (base) input.value = base;
    render();
  });

  wrap.querySelectorAll<HTMLButtonElement>(".subnet-chips .chip").forEach((c) => {
    c.addEventListener("click", () => {
      const v = c.dataset.subnet || "";
      const slash = v.indexOf("/");
      const base = slash >= 0 ? v.slice(0, slash) : v;
      input.value = base;
      const m = v.match(/\/(\d+)$/);
      if (m && Array.from(sel.options).some((o) => Number(o.value) === Number(m[1]))) sel.value = m[1];
      render();
    });
  });

  input.addEventListener("blur", () => {
    let s = input.value.trim();
    if (!s) return;
    // 补全不完整的 IPv4（如 10.88 → 10.88.0.0），再套用下拉前缀。
    const slashIdx = s.indexOf("/");
    const hostPart = (slashIdx >= 0 ? s.slice(0, slashIdx) : s).trim();
    if (/^\d{1,3}(\.\d{1,3}){0,2}$/.test(hostPart)) {
      const parts = hostPart.split(".");
      while (parts.length < 4) parts.push("0");
      s = parts.join(".");
    } else {
      s = hostPart;
    }
    input.value = s;
    const chk = subnetCheck(s, Number(sel.value));
    if (chk.ok) {
      const v = chk.value;
      const normalizedBase = v.indexOf("/") >= 0 ? v.slice(0, v.indexOf("/")) : v;
      if (normalizedBase) input.value = normalizedBase;
    }
    render();
  });

  render();
  return {
    check: () => subnetCheck(input.value.trim(), Number(sel.value)),
    get: () => {
      const c = subnetCheck(input.value.trim(), Number(sel.value));
      return c.ok ? c.value : "";
    },
    render,
  };
}

/* ── 设置（localStorage 默认值） ────────────────── */
const SETTINGS_KEY = "snet.settings";
const LEGACY_CA = "/usr/local/snet/certs/server.pem"; // 旧自签名证书，已被公共证书取代，仅 macOS 曾经使用
const DEFAULT_SETTINGS = {
  server: "https://snet.uizhi.eu.org:8090",
  ca: "",
  wgport: 51820,
};
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

// The coordination server for create/join is the server the daemon is bound
// to (daemon truth). Unbound devices have no server and must bind first.
function currentServer(): string {
  if (status?.bound) return status.serverAddr ?? "";
  return "";
}

/* ── 格式化工具 ────────────────────────────────── */
function fmtBytes(b?: number): string {
  const n = Number(b) || 0;
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/* ── Toast ─────────────────────────────────────── */
function toast(msg: string, kind: "ok" | "err" | "warn" = "ok") {
  const wrap = $("#toast-wrap");
  const el = document.createElement("div");
  el.className = `toast ${kind}`;
  el.textContent = msg;
  wrap.appendChild(el);
  setTimeout(() => {
    el.classList.add("out");
    setTimeout(() => el.remove(), 220);
  }, 2600);
}

/* 复制反馈：按钮显示「已复制」1.4s（控制台同款） */
async function copyText(text: string, btn?: HTMLElement | null) {
  try {
    await navigator.clipboard.writeText(text);
    toast("已复制到剪贴板");
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      toast("已复制到剪贴板");
    } catch {
      toast("复制失败，请手动复制", "err");
    }
    ta.remove();
  }
  if (btn) {
    const old = btn.innerHTML;
    btn.innerHTML = '<span style="color:var(--ok)">已复制</span>';
    setTimeout(() => {
      btn.innerHTML = old;
    }, 1400);
  }
}

/* ── 弹窗框架 ─────────────────────────────────── */
const modalRoot = $("#modal-root");
let modalOnClose: (() => void) | null = null;

type ModalOpts = {
  title: string;
  body: string;
  footer?: string;
  wide?: boolean;
  onBody?: (b: HTMLElement) => void;
  onSave?: (b: HTMLElement) => void;
  onClose?: () => void;
};

function openModal(opts: ModalOpts) {
  modalRoot.innerHTML = `
    <div class="modal-backdrop">
      <div class="modal${opts.wide ? " wide" : ""}" role="dialog" aria-modal="true">
        <div class="modal-head">
          <h3>${esc(opts.title)}</h3>
          <button data-close class="btn icon" title="关闭">✕</button>
        </div>
        <div class="modal-body">${opts.body}</div>
        <div class="modal-foot">
          ${opts.footer ?? ""}
          ${opts.onSave ? `<button id="m-save" class="btn">保存</button>` : ""}
        </div>
      </div>
    </div>`;
  modalRoot.hidden = false;
  modalOnClose = opts.onClose ?? null;
  const el = modalRoot.querySelector<HTMLElement>(".modal")!;
  const backdrop = modalRoot.querySelector(".modal-backdrop")!;
  backdrop.addEventListener("mousedown", (e) => {
    if (e.target === backdrop) closeModal();
  });
  modalRoot.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", closeModal));
  const saveBtn = modalRoot.querySelector<HTMLButtonElement>("#m-save");
  saveBtn?.addEventListener("click", () => {
    opts.onSave?.(el);
    closeModal();
  });
  try {
    opts.onBody?.(el);
  } catch (err) {
    console.error("modal onBody:", err);
  }
  const f = el.querySelector<HTMLElement>("input, select, textarea");
  if (f) setTimeout(() => f.focus(), 30);
}

function closeModal() {
  const cb = modalOnClose;
  modalOnClose = null;
  modalRoot.hidden = true;
  modalRoot.innerHTML = "";
  cb?.();
}

/* 自绘确认弹窗：macOS WKWebView 中 window.confirm() 静默返回 false，
   所以这里用应用内弹窗替代，返回 Promise<boolean>。 */
function confirmDialog(title: string, message: string, danger = false): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false;
    const done = (v: boolean) => {
      if (settled) return;
      settled = true;
      resolve(v);
      closeModal();
    };
    openModal({
      title,
      body: `<p class="confirm-msg">${esc(message)}</p>`,
      footer: `<button id="cd-no" class="btn ghost">取消</button><button id="cd-yes" class="${danger ? "btn danger" : "btn"}">确定</button>`,
      onClose: () => done(false),
      onBody: (m) => {
        m.querySelector("#cd-no")!.addEventListener("click", () => done(false));
        m.querySelector("#cd-yes")!.addEventListener("click", () => done(true));
      },
    });
  });
}

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeModal();
});

/* ── Tab ───────────────────────────────────────── */
document.querySelectorAll(".tab").forEach((t) =>
  t.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((x) => x.classList.toggle("active", x === t));
    const name = (t as HTMLElement).dataset.tab!;
    document.querySelectorAll(".tab-panel").forEach((p) =>
      p.classList.toggle("active", p.id === `tab-${name}`),
    );
  }),
);

/* ── 状态 ──────────────────────────────────────── */
let status: DaemonStatus | null = null;
let onboardingShown = false;

const ONLINE_WINDOW_SEC = 180;
function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}
function onlineCount(n: NetInfo): number {
  const stats = n.peerStats ?? {};
  const now = nowSec();
  return Object.values(stats).filter((p) => p.LastHandshakeSec && now - p.LastHandshakeSec < ONLINE_WINDOW_SEC)
    .length;
}

async function refresh() {
  const btn = $("#btn-refresh") as HTMLButtonElement;
  btn.disabled = true;
  try {
    status = await call<DaemonStatus>("daemon_status");
    await refreshOwnerInfo();
  } catch (e) {
    status = null;
  }
  renderHeader();
  renderNetworks();
  renderStatus();
  maybeShowOnboarding();
  setTimeout(() => {
    btn.disabled = false;
  }, 800);
}

/* 创建者网络的完整信息缓存（成员/待批准数量），每 10s 轮询一次 */
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
        ownerInfo[n.networkId] = await call<NetInfoDetail>("netinfo", { nid: n.networkId });
      } catch {
        delete ownerInfo[n.networkId];
      }
    }),
  );
}

/* 右上角：后台服务 live-dot + 启动按钮 */
function renderHeader() {
  const dot = $("#daemon-state") as HTMLElement;
  if (status) {
    dot.className = "live-dot ok";
    dot.textContent = "后台服务: 运行中";
  } else {
    dot.className = "live-dot err";
    dot.textContent = "后台服务: 未运行";
  }
  const btn = $("#header-svc-btn") as HTMLButtonElement;
  btn.hidden = !!status;
  btn.disabled = false;
  if (!status) {
    btn.textContent = "启动后台服务";
    btn.title = "启动系统后台守护进程（可能弹出管理员密码框）";
    btn.onclick = () => void ensureDaemon();
  }
}

/* ── 我的网络 ──────────────────────────────────── */
function netCard(n: NetInfo): string {
  const linked = !!n.interface;
  const state = linked ? "已链接" : n.active ? "未就绪" : "未链接";
  const cls = linked ? "ok" : n.active ? "warn" : "off";
  const peers = n.peerStats ?? {};
  const total = Object.keys(peers).length;
  const detail = n.owner ? ownerInfo[n.networkId] : undefined;
  const memberTotal = detail ? detail.nodes?.length ?? total + 1 : total + 1;
  const memberOnline = detail
    ? (detail.nodes ?? []).filter((nd) => nd.online).length
    : onlineCount(n) + (linked ? 1 : 0);
  const pendingCount = detail?.pendingCount ?? detail?.pending?.length ?? 0;
  const memberLine =
    memberTotal <= 1
      ? `<span class="muted">暂无其他成员</span>`
      : `<span>成员 <b>${memberOnline}/${memberTotal}</b> 在线</span>`;
  const tx = Object.values(peers).reduce((a, p) => a + (p.TxBytes ?? 0), 0);
  const rx = Object.values(peers).reduce((a, p) => a + (p.RxBytes ?? 0), 0);
  const ops: string[] = [];
  if (n.owner) {
    const memberBtn =
      `<button data-act="members" class="btn ghost sm" title="查看成员列表与在线状态；创建者可批准/拒绝加入请求或踢出成员">成员 ${memberTotal}${pendingCount > 0 ? ` <span class="badge-dot" title="${pendingCount} 个待批准请求">${pendingCount}</span>` : ""}</button>`;
    ops.push(memberBtn);
    ops.unshift(`<button data-act="info" class="btn ghost sm" title="查看服务器上该网络的完整信息（成员、中继、在线状态等）">详情</button>`);
    ops.push(`<button data-act="invite" class="btn ghost sm" title="展示邀请链接与加入二维码，供其他设备扫码加入">邀请</button>`);
    ops.push(`<button data-act="settings" class="btn ghost sm" title="修改网络名称、网段或加入批准设置（仅创建者）">设置</button>`);
    ops.push(`<button data-act="code" class="btn ghost sm" title="查看当前配对码并复制；可作废旧码并生成新码（仅创建者）">查看配对码</button>`);
    ops.push(`<button data-act="delete" class="btn danger ghost sm" title="彻底删除网络：所有成员断开、网段释放，不可恢复（仅创建者）">删除</button>`);
  } else {
    ops.push(`<button data-act="remove" class="btn danger ghost sm" title="本机退出该网络并遗忘配置，需重新扫码加入">退出网络</button>`);
  }
  const err = n.error ? `<p class="msg">${esc(n.error)}</p>` : "";
  return `
  <div class="net" data-nid="${esc(n.networkId)}">
    <div class="net-row">
      <div class="net-main">
        <div class="net-name">${esc(n.name || n.networkId)} ${n.owner ? `<span class="pill owner">owner</span>` : ""}</div>
        <div class="net-meta">
          <span>IP <code>${esc(n.ip ?? "-")}</code></span>
          <span>网段 <code>${esc(n.subnet ?? "-")}</code></span>
          <span>ID <code>${esc(n.networkId)}</code></span>
        </div>
        <div class="net-stats">${memberLine}<span>收 <b>${fmtBytes(rx)}</b></span><span>发 <b>${fmtBytes(tx)}</b></span></div>
      </div>
      <div class="net-side">
        <span class="pill ${cls}">${state}</span>
        <label class="switch" title="${linked ? "断开该网络（保留配置与节点，可随时再连）" : "连接该网络"}">
          <input type="checkbox" data-act="toggle" ${linked ? "checked" : ""} />
          <span class="slider"></span>
        </label>
      </div>
    </div>
    <div class="net-actions">${ops.join("")}</div>
    ${err}
  </div>`;
}

function renderNetworks() {
  const list = $("#net-list");
  const nets = status?.networks ?? [];
  const pending = status?.pendingJoins ?? [];
  if (!status) {
    list.innerHTML = `<div class="empty">
      <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M24 4l18 26H6L24 4z"/><path d="M24 34v6"/><path d="M12 46h24"/></svg>
      <div class="t">后台服务未运行</div>
      <div class="s">需要系统后台守护进程 snetd 维持网络隧道</div>
      <div class="empty-actions"><button id="empty-start" class="btn">启动后台服务</button></div>
    </div>`;
    $("#empty-start").addEventListener("click", ensureDaemon);
    return;
  }
  const hasOwner = nets.some((n) => n.owner);
  // 每客户端仅能创建一个网络：已有 owner 网络时直接隐藏创建入口。
  $("#btn-create").hidden = hasOwner;
  $("#create-limit").hidden = !hasOwner;
  if (!nets.length && !pending.length) {
    list.innerHTML = `<div class="empty">
      <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="24" r="6"/><circle cx="36" cy="24" r="6"/><path d="M18 24h12"/></svg>
      <div class="t">还没有加入任何网络</div>
      <div class="s">创建一个网络，或使用邀请链接/配对码加入其他设备</div>
      <div class="empty-actions">
        <button id="empty-create" class="btn">创建网络</button>
        <button id="empty-join" class="btn ghost">加入网络</button>
      </div>
    </div>`;
    $("#empty-create").addEventListener("click", openCreateModal);
    $("#empty-join").addEventListener("click", openJoinModal);
    return;
  }
  const netHTML = nets.map(netCard).join("");
  const pendingHTML = pending.length
    ? `<h4 class="pending-heading">待批准 · 加入请求</h4>` +
      pending
        .map(
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
        )
        .join("")
    : "";
  list.innerHTML = netHTML + pendingHTML;

  const cnt = $("#cnt-net");
  cnt.textContent = String(nets.length);
  cnt.className = "cnt" + (pending.length ? " hot" : "");
  cnt.title = pending.length ? `${pending.length} 个待批准请求` : "";
}

/* 网络卡片事件（事件委托） */
const netList = $("#net-list");

netList.addEventListener("click", async (e) => {
  const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-act]");
  if (!btn) return;
  const card = btn.closest<HTMLElement>(".net");
  if (!card) return;
  if (btn.dataset.act === "cancel-pending") {
    if (!(await confirmDialog("取消加入请求", "取消等待批准？", true))) return;
    try {
      await call("cancel_pending", { pendingId: card.dataset.pid });
      toast("已取消加入请求");
    } catch (err) {
      toast(String(err), "err");
    }
    await refresh();
    return;
  }
  await onAction(btn.dataset.act!, card.dataset.nid!, btn);
});

netList.addEventListener("change", async (e) => {
  const input = e.target as HTMLInputElement;
  if (input.dataset.act !== "toggle") return;
  const card = input.closest<HTMLElement>(".net");
  if (!card) return;
  await onToggle(card, input.checked);
});

async function onToggle(card: HTMLElement, checked: boolean) {
  const nid = card.dataset.nid!;
  const input = card.querySelector<HTMLInputElement>('input[data-act="toggle"]')!;
  input.disabled = true;
  card.classList.add("busy");
  try {
    if (checked) await call("rejoin_nid", { nid });
    else await call("leave_nid", { nid });
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
  const setPending = (t: string) => {
    btn.innerHTML = `${SPIN} ${t}`;
  };
  try {
    switch (act) {
      case "info": {
        setPending("查询中…");
        const r = await call<unknown>("netinfo", { nid });
        openModal({
          title: `网络详情 · ${nid}`,
          body: `<pre class="json">${esc(JSON.stringify(r, null, 2))}</pre>`,
          wide: true,
        });
        break;
      }
      case "members": {
        setPending("查询中…");
        await showMembers(nid);
        break;
      }
      case "settings": {
        await openNetworkSettings(nid);
        break;
      }
      case "invite": {
        setPending("生成中…");
        await openInviteModal(nid);
        break;
      }
      case "code": {
        setPending("查询中…");
        await openCodeModal(nid);
        break;
      }
      case "delete": {
        if (!(await confirmDialog("删除网络", "删除将断开所有成员并释放网段，不可恢复。确定？", true))) return;
        setPending("删除中…");
        await call("delete_nid", { nid });
        toast("网络已删除");
        break;
      }
      case "remove": {
        if (!(await confirmDialog("退出网络", "退出后需重新扫码加入。继续？", true))) return;
        setPending("退出中…");
        await call("remove_nid", { nid });
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

async function openNetworkSettings(nid: string) {
  let detail: NetInfoDetail | undefined = ownerInfo[nid];
  if (!detail) {
    try {
      detail = await call<NetInfoDetail>("netinfo", { nid });
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
      <div class="row"><label>网络名称</label><input id="s-name" value="${esc(curName)}" placeholder="留空保持不变" /></div>
      <div class="row"><label>网段</label>${subnetWidgetHTML({ id: "s-subnet", value: curSubnet, placeholder: "留空保持不变", emptyHint: "留空保持不变" })}</div>
      <div class="row"><label class="inline"><input id="s-approval" type="checkbox" ${detail.approvalRequired ? "checked" : ""} /> 新成员加入需创建者批准</label></div>
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
        const result = body.querySelector<HTMLElement>("#s-result")!;
        const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
        submit.disabled = true;
        result.className = "msg";
        result.textContent = "保存中…";
        try {
          await call("update_settings", {
            nid,
            name,
            subnet: subnet !== curSubnet ? subnet : "",
            approvalRequired: approval !== !!detail!.approvalRequired ? approval : null,
          });
          result.className = "msg ok";
          result.textContent = "已保存";
          await refreshOwnerInfo(true);
          toast("网络设置已保存");
          closeModal();
        } catch (e) {
          result.className = "msg";
          result.textContent = `保存失败: ${e}`;
          submit.disabled = false;
        }
      });
    },
  });
}

async function openCodeModal(nid: string) {
  let code = "";
  try {
    const detail = await call<NetInfoDetail>("netinfo", { nid });
    code = detail.pairingCode || "";
  } catch (e) {
    toast(`获取配对码失败: ${e}`, "err");
    return;
  }
  if (!code) {
    // 服务器重启后配对码明文不落盘；此时自动生成新码（旧码作废）。
    try {
      const r = await call<{ pairingCode: string }>("reset_code", { nid });
      code = r.pairingCode;
      toast("原配对码不可用，已生成新码（旧码作废）", "warn");
    } catch (e) {
      toast(`生成配对码失败: ${e}`, "err");
      return;
    }
  }
  const name = nid;
  openModal({
    title: `查看配对码 · ${name}`,
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
          const r = await call<{ pairingCode: string }>("reset_code", { nid });
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

async function openInviteModal(nid: string) {
  let detail: NetInfoDetail | undefined;
  try {
    detail = await call<NetInfoDetail>("netinfo", { nid });
  } catch (e) {
    toast(`获取邀请信息失败: ${e}`, "err");
    return;
  }
  let code = detail.pairingCode;
  if (!code) {
    // 服务器重启后配对码明文不落盘；此时自动生成新码（旧码作废）。
    try {
      const r = await call<{ pairingCode: string }>("reset_code", { nid });
      code = r.pairingCode;
      toast("原配对码不可用，已生成新码（旧码作废）", "warn");
    } catch (e) {
      toast(`生成配对码失败: ${e}`, "err");
      return;
    }
  }
  const netId = detail.id || detail.networkId;
  if (!netId) {
    toast("获取网络ID失败", "err");
    return;
  }
  const link = `snet://join?nid=${encodeURIComponent(netId)}&code=${encodeURIComponent(code)}`;
  const name = detail.name || netId;
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
        const b = e.currentTarget as HTMLButtonElement;
        await copyText(link, b);
      });
    },
  });
}

async function showMembers(nid: string) {
  const mine = (status?.networks ?? []).find((n) => n.networkId === nid);
  const myIp = mine?.ip;
  if (mine?.owner) {
    const r = await call<NetInfoDetail>("netinfo", { nid });
    ownerInfo[nid] = r;
    const rows = (r.nodes ?? [])
      .map((nd) => {
        const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
        const kick =
          nd.ip === myIp ? `<span class="muted">自己</span>` : `<button data-node="${esc(nd.id)}" class="btn danger ghost sm">踢出</button>`;
        return `<tr>
          <td class="mono">${esc(nd.ip)}</td>
          <td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td>
          <td>${online}</td>
          <td>${kick}</td>
        </tr>`;
      })
      .join("");
    const pendingRows = (r.pending ?? [])
      .map((p) => {
        return `<tr>
          <td colspan="2"><span class="muted">设备</span> <code>${p.deviceId ? esc(p.deviceId.slice(0, 8)) + "…" : "-"}</code><span class="muted"> 公钥</span> <code>${esc(p.publicKey.slice(0, 12))}…</code></td>
          <td><span class="pill warn">待批准</span></td>
          <td><button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">批准</button> <button data-pend="${esc(p.id)}" class="btn danger sm">拒绝</button></td>
        </tr>`;
      })
      .join("");
    const pendingSection = pendingRows
      ? `<div class="pending-block"><h4>待批准加入请求</h4><div class="tbl-wrap"><table class="members"><tbody>${pendingRows}</tbody></table></div></div>`
      : "";
    openModal({
      title: `成员 · ${nid}`,
      wide: true,
      body: `<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>${pendingSection}`,
      onBody: (b) => {
        b.querySelectorAll<HTMLElement>("[data-node]").forEach((k) =>
          k.addEventListener("click", async () => {
            if (!(await confirmDialog("踢出成员", "踢出该成员？该设备将立即断开。", true))) return;
            const btn = k as HTMLButtonElement;
            const orig = btn.textContent;
            btn.disabled = true;
            btn.textContent = "踢出中…";
            try {
              await call("kick_nid", { nid, nodeId: k.dataset.node });
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
              if (deny) await call("deny_pending", { nid, pendingId: pid });
              else await call("approve_pending", { nid, pendingId: pid });
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
  const r = await call<PeersResp>("peers", { nid });
  const all: NetNode[] = r.self ? [r.self, ...(r.peers ?? [])] : (r.peers ?? []);
  const rows = all
    .map((nd) => {
      const isSelf = nd.ip === myIp;
      const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
      return `<tr>
        <td class="mono">${esc(nd.ip)}</td>
        <td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td>
        <td>${online}</td>
        <td>${isSelf ? `<span class="muted">自己</span>` : ""}</td>
      </tr>`;
    })
    .join("");
  openModal({
    title: `成员 · ${nid}`,
    wide: true,
    body: `<p class="hint">成员列表（只读，本机非创建者）</p><div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`,
  });
}

/* ── 创建弹窗 ──────────────────────────────────── */
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
      ${serverHint}
      <div class="row"><label>WireGuard 端口</label><input id="m-port" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="row"><label>CA 证书路径</label><input id="m-ca" type="text" value="${esc(s.ca)}" placeholder="公共证书(如 Let's Encrypt)留空；自签名服务器填证书路径" /></div>
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
          const server = currentServer();
          const port = Number((body.querySelector("#m-port") as HTMLInputElement).value);
          const ca = (body.querySelector("#m-ca") as HTMLInputElement).value.trim();
          if (!name) throw new Error("请输入网络名称");
          if (!server) throw new Error("未连接服务器：请先在设置中链接服务器");
          const r = await call<{ networkId: string; ip: string; pairingCode: string; link: string }>(
            "create_network",
            { server, port, ca, name, subnet, approvalRequired: false },
          );
          // 创建成功后整个弹窗替换为邀请信息：隐藏创建表单与创建/取消按钮，
          // 只保留二维码与邀请信息，避免挤在底部被忽略。
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

/* ── 加入弹窗 ──────────────────────────────────── */
// Strip trailing slashes so a link server matches the daemon's normalized form.
function normalizeServerCompare(s: string): string {
  return s.replace(/\/+$/, "");
}

// Extract the optional server an invite link carries, if any.
function linkServerOf(link: string): string | undefined {
  try {
    const u = new URL(link);
    if (u.protocol === "snet:" || u.protocol === "http:" || u.protocol === "https:") {
      return u.searchParams.get("server") || undefined;
    }
  } catch {
    /* not a URL → manual nid:code input */
  }
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
    body: `<div class="row"><label>邀请链接</label><input id="m-link" placeholder="snet://join?nid=...&code=..." /></div>
      <p class="hint" style="text-align:center">或手动输入</p>
      <div class="row"><label>网络ID</label><input id="m-nid" type="text" placeholder="6 位网络ID" /></div>
      <div class="row"><label>配对码</label><input id="m-code" type="text" placeholder="12 位配对码" /></div>
      ${serverHint}
      <div class="row"><label>WireGuard 端口</label><input id="m-port" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="row"><label>CA 证书路径</label><input id="m-ca" type="text" value="${esc(s.ca)}" placeholder="公共证书(如 Let's Encrypt)留空；自签名服务器填证书路径" /></div>
      <div id="m-bind-auth" hidden></div>
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">加入</button>`,
    onBody: (body) => {
      const submit = body.querySelector<HTMLButtonElement>("#m-submit")!;
      const result = body.querySelector<HTMLElement>("#m-result")!;
      const authWrap = body.querySelector<HTMLElement>("#m-bind-auth")!;
      const caInput = body.querySelector<HTMLInputElement>("#m-ca")!;

      // Bind this device to the link's server (auth code required), then run
      // the join. Called only when the device is not already bound to that server.
      const bindAndThen = (server: string, then: () => Promise<void>) => {
        authWrap.hidden = false;
        authWrap.innerHTML = `
          <div class="settings-block">
            <p class="msg">该邀请属于服务器 <code>${esc(server)}</code>，本机尚未绑定该服务器。请输入设备授权码以绑定后加入：</p>
            <div class="row"><label>设备授权码</label><input id="m-bind-code" placeholder="管理端生成的授权码" autocomplete="off" /></div>
            <div class="settings-actions"><button id="m-bind-go" class="btn">绑定并加入</button></div>
            <p class="msg" id="m-bind-result"></p>
          </div>`;
        const codeInput = authWrap.querySelector<HTMLInputElement>("#m-bind-code")!;
        const bindResult = authWrap.querySelector<HTMLElement>("#m-bind-result")!;
        const bindBtn = authWrap.querySelector<HTMLButtonElement>("#m-bind-go")!;
        codeInput.focus();
        const go = async () => {
          const code = codeInput.value.trim();
          if (!code) {
            bindResult.className = "msg";
            bindResult.textContent = "请输入设备授权码";
            return;
          }
          bindBtn.disabled = true;
          bindResult.className = "msg";
          bindResult.textContent = "正在绑定…";
          try {
            const ca = caInput.value.trim();
            await call("bind_server", { server, ca, code });
            saveSettings({ ...loadSettings(), server, ca });
            bindResult.className = "msg ok";
            bindResult.textContent = `已绑定 ${server}，正在加入…`;
            await then();
          } catch (e) {
            bindResult.className = "msg";
            bindResult.textContent = `绑定失败: ${e}`;
            bindBtn.disabled = false;
          }
        };
        bindBtn.addEventListener("click", go);
        codeInput.addEventListener("keydown", (e) => {
          if (e.key === "Enter") void go();
        });
      };

      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = `${SPIN} 加入中…`;
        result.className = "msg";
        result.textContent = "";
        try {
          const port = Number((body.querySelector("#m-port") as HTMLInputElement).value);
          const ca = caInput.value.trim();
          let link = (body.querySelector("#m-link") as HTMLInputElement).value.trim();
          if (!link) {
            const nid = (body.querySelector("#m-nid") as HTMLInputElement).value.trim();
            const code = (body.querySelector("#m-code") as HTMLInputElement).value.trim();
            if (!nid || !code) throw new Error("请输入邀请链接，或网络ID + 配对码");
            link = `snet://join?nid=${nid}&code=${code}`;
          }
          const linkServer = linkServerOf(link);
          const srv = currentServer();

          // Throw this sentinel when the server enforces the device gate and
          // this device is not bound to it yet; the caller then prompts for a
          // device authorization code and retries.
          class NeedBind extends Error {}
          const doJoin = async () => {
            const server = linkServer && linkServer.trim() ? linkServer : srv;
            if (!server) throw new Error("未连接服务器：请先在设置中链接服务器");
            try {
              const r = await call<{ ip?: string; networkId?: string; status?: string; pendingId?: string }>(
                "join_network",
                { server, port, ca, link },
              );
              result.className = "msg ok";
              if (r.status === "pending") {
                result.innerHTML = `<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>`;
              } else {
                result.textContent = `已加入: IP ${r.ip ?? "-"}，网络 ${r.networkId ?? "-"}`;
              }
              await refresh();
            } catch (e) {
              // the enrollment gate refused an unbound device; escalate to the
              // authorization-code bind flow instead of failing outright
              if (String(e).includes("设备未授权")) throw new NeedBind();
              throw e;
            }
          };

          if (linkServer && srv && normalizeServerCompare(linkServer) === normalizeServerCompare(srv)) {
            // already bound to the link's server: join directly
            await doJoin();
          } else if (linkServer) {
            // not bound to the link's server: non-enforcement servers accept a
            // direct join; enforcement servers refuse it, so bind first
            try {
              await doJoin();
            } catch (e) {
              if (e instanceof NeedBind) {
                await new Promise<void>((resolve, reject) => {
                  bindAndThen(linkServer, async () => {
                    try {
                      await doJoin();
                      resolve();
                    } catch (e2) {
                      result.className = "msg";
                      result.textContent = `加入失败: ${e2}`;
                      submit.disabled = false;
                      reject(e2);
                    }
                  });
                });
              } else {
                throw e;
              }
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

/* ── 设置弹窗 ──────────────────────────────────── */
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
  const helpRows = HELP_ROWS.map(
    ([op, who, desc]) => `<tr><td>${op}</td><td>${who}</td><td>${desc}</td></tr>`,
  ).join("");
  openModal({
    title: "设置",
    body: `<div class="settings-block">
        <div class="row"><label>服务器地址</label><input id="s-server" value="${esc(s.server)}" placeholder="https://example.com:8090" /></div>
        <div class="row"><label>设备授权码</label><input id="s-code" placeholder="管理端生成的设备授权码（仅用于链接，不保存）" autocomplete="off" /></div>
        <div class="row"><label>CA 证书路径</label><input id="s-ca" value="${esc(s.ca)}" placeholder="公共证书(如 Let's Encrypt)留空；自签名服务器填证书路径" /></div>
        <div class="settings-actions">
          <button id="s-bind" class="btn">链接服务器</button>
        </div>
        <p class="msg" id="s-bind-result"></p>
      </div>
      <div class="row"><label>WireGuard 端口</label><input id="s-wgport" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="settings-actions" id="s-daemon-row">
        ${status
          ? `<span class="live-dot ok">后台服务: 运行中</span>`
          : `<button id="s-start-daemon" class="btn ghost">启动后台服务</button>`}
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

      // 链接服务器（绑定设备授权码，成功后提示并刷新状态）
      body.querySelector("#s-bind")?.addEventListener("click", async () => {
        const server = (body.querySelector("#s-server") as HTMLInputElement).value.trim();
        const ca = (body.querySelector("#s-ca") as HTMLInputElement).value.trim();
        const code = (body.querySelector("#s-code") as HTMLInputElement).value.trim();
        if (!server) {
          bindResult.className = "msg";
          bindResult.textContent = "请输入服务器地址";
          return;
        }
        if (!code) {
          bindResult.className = "msg";
          bindResult.textContent = "请输入设备授权码";
          return;
        }
        bindResult.className = "msg";
        bindResult.textContent = "正在链接…";
        try {
          await call("bind_server", { server, ca, code });
          saveSettings({ ...loadSettings(), server, ca });
          bindResult.className = "msg ok";
          bindResult.textContent = `已绑定 ${server}`;
          toast("已绑定服务器");
          await refresh();
        } catch (e) {
          bindResult.className = "msg";
          bindResult.textContent = `链接失败: ${e}`;
        }
      });

      body.querySelector("#s-start-daemon")?.addEventListener("click", async () => {
        msg.className = "msg";
        msg.textContent = "正在启动后台服务（可能弹出管理员密码框）…";
        try {
          await call("ensure_daemon");
          msg.className = "msg ok";
          msg.textContent = "后台服务已就绪";
          await refresh();
          const row = body.querySelector("#s-daemon-row");
          if (row) row.innerHTML = '<span class="live-dot ok">后台服务: 运行中</span>';
        } catch (e) {
          msg.className = "msg";
          msg.textContent = String(e);
        }
      });
    },
  });
}

/* ── 首次使用引导 ──────────────────────────────── */
// 全新未绑定设备进入主界面时显示覆盖式引导，提供「链接服务器」路径与带提示
// 的跳过入口。
function maybeShowOnboarding() {
  if (onboardingShown) return;
  // A device already bound or already holding networks or pending joins is
  // already using a server: it must not get the overlay.
  if (status && status.bound) return;
  if (status && (status.networks?.length || status.pendingJoins?.length)) return;
  onboardingShown = true;
  renderOnboarding();
}

function renderOnboarding() {
  const ov = $("#onboarding");
  const s = loadSettings();
  ($("#onb-server") as HTMLInputElement).value = s.server;
  ($("#onb-ca") as HTMLInputElement).value = s.ca;
  $("#onb-daemon").hidden = !!status;

  const daemonResult = $("#onb-daemon-result") as HTMLElement;
  const startDaemon = $("#onb-start-daemon") as HTMLButtonElement;
  startDaemon.onclick = async () => {
    startDaemon.disabled = true;
    startDaemon.textContent = "启动中…";
    daemonResult.className = "msg";
    daemonResult.textContent = "正在启动后台服务（可能弹出管理员密码框）…";
    try {
      await call("ensure_daemon");
      daemonResult.className = "msg ok";
      daemonResult.textContent = "后台服务已就绪";
      await refresh();
      $("#onb-daemon").hidden = true;
    } catch (e) {
      daemonResult.className = "msg";
      daemonResult.textContent = `启动失败: ${e}`;
    } finally {
      startDaemon.disabled = false;
      startDaemon.textContent = "启动后台服务";
    }
  };

  const bindResult = $("#onb-bind-result") as HTMLElement;
  ($("#onb-bind") as HTMLButtonElement).onclick = async () => {
    const server = ($("#onb-server") as HTMLInputElement).value.trim();
    const ca = ($("#onb-ca") as HTMLInputElement).value.trim();
    const code = ($("#onb-code") as HTMLInputElement).value.trim();
    if (!server) {
      bindResult.className = "msg";
      bindResult.textContent = "请输入服务器地址";
      return;
    }
    if (!code) {
      bindResult.className = "msg";
      bindResult.textContent = "请输入设备授权码";
      return;
    }
    bindResult.className = "msg";
    bindResult.textContent = "正在绑定…";
    try {
      await call("bind_server", { server, ca, code });
      saveSettings({ ...loadSettings(), server, ca });
      bindResult.className = "msg ok";
      bindResult.textContent = `已绑定 ${server}`;
      toast("已绑定服务器");
      await finishOnboarding();
    } catch (e) {
      bindResult.className = "msg";
      bindResult.textContent = `绑定失败: ${e}`;
    }
  };

  ($("#onb-skip") as HTMLAnchorElement).onclick = (e) => {
    e.preventDefault();
    finishOnboarding();
  };

  ov.hidden = false;
}

async function finishOnboarding() {
  $("#onboarding").hidden = true;
  await refresh();
}

/* ── 状态页 ────────────────────────────────────── */
function renderStatus() {
  if (!status) {
    $("#device-id").textContent = "-";
    $("#svc-server").textContent = "-";
    $("#svc-wgport").textContent = "-";
    $("#tunnel-detail").innerHTML = `<p class="muted">后台服务未运行</p>`;
    $("#status-json").textContent = "(未连接后台服务)";
    return;
  }
  $("#device-id").textContent = status.deviceId ?? "-";
  const addr = status.serverAddr ?? "";
  let svcText: string;
  if (status.bound && addr) {
    svcText = `${addr}`;
  } else {
    svcText = "未连接服务器";
  }
  $("#svc-server").textContent = svcText;
  $("#svc-wgport").textContent = String(status.wgPort ?? "-");
  const nets = status.networks ?? [];
  $("#tunnel-detail").innerHTML = nets.length
    ? nets
        .map((n) => {
          const stats = Object.values(n.peerStats ?? {});
          const bytes = stats.reduce((a, p) => a + (p.RxBytes ?? 0) + (p.TxBytes ?? 0), 0);
          const peers = stats.length;
          const linked = !!n.interface;
          return `<div class="kv"><span>${esc(n.name || n.networkId)}</span>
            <code>${esc(n.interface || "无隧道")}</code>
            <span class="muted">收/发 ${fmtBytes(bytes)} · 成员 ${peers ? `${onlineCount(n)}/${peers} 在线` : "暂无其他成员"}</span>
            ${linked ? '<span class="pill ok">已链接</span>' : '<span class="pill off">未链接</span>'}
          </div>`;
        })
        .join("")
    : `<p class="muted">未加入任何网络</p>`;
  $("#status-json").textContent = JSON.stringify(status, null, 2);
}

async function ensureDaemon() {
  const btn = $("#header-svc-btn") as HTMLButtonElement;
  btn.disabled = true;
  btn.textContent = "启动中…";
  try {
    await call("ensure_daemon");
    toast("后台服务已就绪");
  } catch (e) {
    toast(String(e), "err");
  } finally {
    btn.disabled = false;
    await refresh();
  }
}

/* ── 事件绑定 ──────────────────────────────────── */
$("#btn-settings").addEventListener("click", openSettingsModal);
$("#btn-create").addEventListener("click", openCreateModal);
$("#btn-join").addEventListener("click", openJoinModal);
$("#btn-refresh").addEventListener("click", () => void refresh());
$("#btn-leave-all").addEventListener("click", async () => {
  try {
    await call("leave_nid", { nid: "" });
    toast("已停止全部网络");
  } catch (e) {
    toast(String(e), "err");
  }
  await refresh();
});

refresh();
setInterval(() => {
  if (!document.hidden) refresh();
}, 3000);
