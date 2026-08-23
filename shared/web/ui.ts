/* ── Shared UI logic for SNET client ──────────────────────────────
 * This module is the single source of truth for the client UI.
 * Platform adapters (Desktop/Web/Android) provide a Backend implementation
 * and call init() to wire everything up.
 */
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "./types.js";
import { $, esc, SPIN, fmtBytes, onlineCount, toast, copyText, renderQR, loadQR, openModal, closeModal, confirmDialog, initTabs } from "./utils.js";
import { subnetCheck, subnetWidgetHTML, subnetWidgetInit } from "./subnet.js";

let backend: Backend;
let status: DaemonStatus | null = null;
let onboardingShown = false;

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
async function refresh() {
  const btn = $("#btn-refresh") as HTMLButtonElement;
  if (btn) btn.disabled = true;
  try {
    status = await backend.status();
    await refreshOwnerInfo();
  } catch {
    status = null;
  }
  renderHeader();
  renderNetworks();
  renderStatus();
  maybeShowOnboarding();
  if (btn) setTimeout(() => { btn.disabled = false; }, 800);
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

/* ── Header ───────────────────────────────────────────────────── */
function renderHeader() {
  const dot = $("#daemon-state") as HTMLElement;
  if (!dot) return;
  if (backend.hasDaemonControl) {
    if (status) {
      dot.className = "live-dot ok";
      dot.textContent = "后台服务: 运行中";
    } else {
      dot.className = "live-dot err";
      dot.textContent = "后台服务: 未运行";
    }
    const btn = $("#header-svc-btn") as HTMLButtonElement;
    if (btn) {
      btn.hidden = !!status;
      btn.disabled = false;
      if (!status) {
        btn.textContent = "启动后台服务";
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
function netCard(n: { networkId: string; name?: string; ip?: string; subnet?: string; interface?: string; active?: boolean; owner?: boolean; error?: string; peerStats?: Record<string, { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number }>; allowedSubnets?: string[] }): string {
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
    ops.push(`<button data-act="info" class="btn ghost sm" title="查看服务器上该网络的完整信息">详情</button>`);
    ops.push(`<button data-act="members" class="btn ghost sm" title="查看成员列表与在线状态">成员 ${memberTotal}${pendingCount > 0 ? ` <span class="badge-dot" title="${pendingCount} 个待批准请求">${pendingCount}</span>` : ""}</button>`);
    ops.push(`<button data-act="invite" class="btn ghost sm" title="展示邀请链接与加入二维码">邀请</button>`);
    ops.push(`<button data-act="settings" class="btn ghost sm" title="修改网络名称、网段或加入批准设置">设置</button>`);
    ops.push(`<button data-act="subnets" class="btn ghost sm" title="宣告本设备的局域网子网">子网路由</button>`);
    ops.push(`<button data-act="code" class="btn ghost sm" title="查看当前配对码并复制">查看配对码</button>`);
    ops.push(`<button data-act="delete" class="btn danger ghost sm" title="彻底删除网络">删除</button>`);
  } else {
    ops.push(`<button data-act="subnets" class="btn ghost sm" title="宣告本设备的局域网子网">子网路由</button>`);
    ops.push(`<button data-act="remove" class="btn danger ghost sm" title="本机退出该网络并遗忘配置">退出网络</button>`);
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
          ${(n.allowedSubnets?.length ?? 0) > 0 ? `<span class="pill subnet-route">路由 ${n.allowedSubnets!.length} 个子网</span>` : ""}
        </div>
        <div class="net-stats">${memberLine}<span>收 <b>${fmtBytes(rx)}</b></span><span>发 <b>${fmtBytes(tx)}</b></span></div>
      </div>
      <div class="net-side">
        <span class="pill ${cls}">${state}</span>
        <label class="switch" title="${linked ? "断开该网络" : "连接该网络"}">
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
  if (!list) return;
  const nets = status?.networks ?? [];
  const pending = status?.pendingJoins ?? [];
  if (!status) {
    list.innerHTML = `<div class="empty">
      <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M24 4l18 26H6L24 4z"/><path d="M24 34v6"/><path d="M12 46h24"/></svg>
      <div class="t">后台服务未运行</div>
      <div class="s">需要系统后台守护进程 snetd 维持网络隧道</div>
      <div class="empty-actions">${backend.hasDaemonControl ? `<button id="empty-start" class="btn">启动后台服务</button>` : ""}</div>
    </div>`;
    const startBtn = $("#empty-start");
    if (startBtn) startBtn.addEventListener("click", ensureDaemon);
    return;
  }
  const hasOwner = nets.some((n) => n.owner);
  const btnCreate = $("#btn-create");
  const createLimit = $("#create-limit");
  if (btnCreate) btnCreate.hidden = hasOwner;
  if (createLimit) createLimit.hidden = !hasOwner;
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
    $("#empty-create")?.addEventListener("click", openCreateModal);
    $("#empty-join")?.addEventListener("click", openJoinModal);
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
  list.innerHTML = netHTML + pendingHTML;
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
    const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-act]");
    if (!btn) return;
    const card = btn.closest<HTMLElement>(".net");
    if (!card) return;
    if (btn.dataset.act === "cancel-pending") {
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
    await onAction(btn.dataset.act!, card.dataset.nid!, btn);
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
  input.disabled = true;
  card.classList.add("busy");
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
        await copyText(link, e.currentTarget as HTMLButtonElement);
      });
    },
  });
}

/* ── Members modal ────────────────────────────────────────────── */
async function showMembers(nid: string) {
  const mine = (status?.networks ?? []).find((n) => n.networkId === nid);
  const myIp = mine?.ip;
  if (mine?.owner) {
    const r = await backend.netinfo(nid);
    ownerInfo[nid] = r;
    const rows = (r.nodes ?? []).map((nd) => {
      const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
      const kick = nd.ip === myIp ? `<span class="muted">自己</span>` : `<button data-node="${esc(nd.id)}" class="btn danger ghost sm">踢出</button>`;
      const subnets = (nd.allowedSubnets?.length ?? 0) > 0
        ? `<span class="pill subnet-route">${esc(nd.allowedSubnets!.join(", "))}</span>`
        : `<span class="muted">-</span>`;
      return `<tr><td class="mono">${esc(nd.ip)}</td><td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td><td>${online}</td><td>${subnets}</td><td>${kick}</td></tr>`;
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
      body: `<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>子网路由</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>${pendingSection}`,
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
      ? `<span class="pill subnet-route">${esc(nd.allowedSubnets!.join(", "))}</span>`
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
          const r: CreateResp = await backend.create({ server, port, ca, name, subnet, approvalRequired: false });
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
    body: `<div class="row"><label>邀请链接</label><input id="m-link" type="text" placeholder="snet://join?nid=...&code=..." /></div>
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
            const ca = caInput.value.trim();
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

          class NeedBind extends Error {}
          const doJoin = async () => {
            const server = linkServer && linkServer.trim() ? linkServer : srv;
            if (!server) throw new Error("未连接服务器：请先在设置中链接服务器");
            try {
              const r: JoinResp = await backend.join({ server, port, ca, link });
              result.className = "msg ok";
              if (r.status === "pending") {
                result.innerHTML = `<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>`;
              } else {
                result.textContent = `已加入: IP ${r.ip ?? "-"}，网络 ${r.networkId ?? "-"}`;
              }
              await refresh();
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
          ? (status ? `<span class="live-dot ok">后台服务: 运行中</span>` : `<button id="s-start-daemon" class="btn ghost">启动后台服务</button>`)
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

      body.querySelector("#s-bind")?.addEventListener("click", async () => {
        const server = (body.querySelector("#s-server") as HTMLInputElement).value.trim();
        const ca = (body.querySelector("#s-ca") as HTMLInputElement).value.trim();
        const code = (body.querySelector("#s-code") as HTMLInputElement).value.trim();
        if (!server) { bindResult.className = "msg"; bindResult.textContent = "请输入服务器地址"; return; }
        if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
        bindResult.className = "msg"; bindResult.textContent = "正在链接…";
        try {
          await backend.bind({ server, ca, code });
          saveSettings({ ...loadSettings(), server, ca });
          bindResult.className = "msg ok"; bindResult.textContent = `已绑定 ${server}`;
          toast("已绑定服务器");
          await refresh();
        } catch (e) {
          bindResult.className = "msg"; bindResult.textContent = `链接失败: ${e}`;
        }
      });

      body.querySelector("#s-start-daemon")?.addEventListener("click", async () => {
        msg.className = "msg";
        msg.textContent = "正在启动后台服务（可能弹出管理员密码框）…";
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
      daemonResult.textContent = "正在启动后台服务（可能弹出管理员密码框）…";
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
        startDaemon.textContent = "启动后台服务";
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
    if (tunnelDetail) tunnelDetail.innerHTML = `<p class="muted">后台服务未运行</p>`;
    if (statusJson) statusJson.textContent = "(未连接后台服务)";
    return;
  }
  if (deviceId) deviceId.textContent = status.deviceId ?? "-";
  const addr = status.serverAddr ?? "";
  if (svcServer) svcServer.textContent = status.bound && addr ? addr : "未连接服务器";
  if (svcWgport) svcWgport.textContent = String(status.wgPort ?? "-");
  const nets = status.networks ?? [];
  if (tunnelDetail) {
    tunnelDetail.innerHTML = nets.length
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
  $("#btn-refresh")?.addEventListener("click", () => void refresh());
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
  refresh();
  setInterval(() => {
    if (!document.hidden) refresh();
  }, 3000);
}
