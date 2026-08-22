/* ── snetd Web GUI — app.js ────────────────────────────────────── */
"use strict";

/* ── constants ─────────────────────────────────────────────────── */
const SPIN = '<span class="spinner"></span>';
const NS = (window.__NS__ = "/ctl");

/* ── state ─────────────────────────────────────────────────────── */
let status = null;
let $ = (s, p) => (p || document).querySelector(s);

/* ── esc ───────────────────────────────────────────────────────── */
function esc(s) {
  const d = document.createElement("div");
  d.textContent = s ?? "";
  return d.innerHTML;
}

/* ── toast ─────────────────────────────────────────────────────── */
function toast(msg, cls) {
  const w = $("#toast-wrap");
  const el = document.createElement("div");
  el.className = "toast" + (cls ? " " + cls : "");
  el.textContent = msg;
  w.appendChild(el);
  setTimeout(() => { el.classList.add("out"); setTimeout(() => el.remove(), 250); }, 2800);
}

/* ── confirm dialog ────────────────────────────────────────────── */
function confirmDialog(title, message, danger) {
  return new Promise((resolve) => {
    const root = $("#modal-root");
    root.hidden = false;
    root.innerHTML = `<div class="modal-backdrop"><div class="modal confirm-modal">
      <div class="modal-head"><h3>${esc(title)}</h3><button class="btn icon" data-close>&times;</button></div>
      <div class="modal-body"><p class="msg">${esc(message)}</p></div>
      <div class="modal-foot">
        <button class="btn ghost" data-action="cancel">取消</button>
        <button class="btn ${danger ? "danger" : ""}" data-action="ok">确定</button>
      </div>
    </div></div>`;
    const bk = root.querySelector(".modal-backdrop");
    const close = (v) => { root.hidden = true; root.innerHTML = ""; resolve(v); };
    bk.addEventListener("click", (e) => { if (e.target === bk) close(false); });
    root.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", () => close(false)));
    root.querySelector("[data-action='cancel']").addEventListener("click", () => close(false));
    root.querySelector("[data-action='ok']").addEventListener("click", () => close(true));
  });
}

/* ── modals ────────────────────────────────────────────────────── */
function openModal({ title, body, footer, wide, onBody }) {
  const root = $("#modal-root");
  root.hidden = false;
  root.innerHTML = `<div class="modal-backdrop"><div class="modal${wide ? " wide" : ""}">
    <div class="modal-head"><h3>${esc(title)}</h3><button class="btn icon" data-close>&times;</button></div>
    <div class="modal-body">${body}</div>
    ${footer ? `<div class="modal-foot">${footer}</div>` : ""}
  </div></div>`;
  const bk = root.querySelector(".modal-backdrop");
  bk.addEventListener("click", (e) => { if (e.target === bk) closeModal(); });
  root.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", closeModal));
  if (onBody) onBody(root.querySelector(".modal"));
}
function closeModal() {
  const r = $("#modal-root");
  r.hidden = true;
  r.innerHTML = "";
}

/* ── QR ────────────────────────────────────────────────────────── */
function renderQR(el, text) {
  el.textContent = "";
  const url = "https://api.qrserver.com/v1/create-qr-code/?size=160x160&data=" + encodeURIComponent(text);
  const img = document.createElement("img");
  img.src = url;
  img.alt = "QR";
  img.style.width = "160px";
  img.style.borderRadius = "8px";
  el.appendChild(img);
}

/* ── helpers ───────────────────────────────────────────────────── */
function fmtBytes(b) {
  if (!b) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = b;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(i ? 1 : 0) + " " + u[i];
}
function onlineCount(n) {
  return Object.values(n.peerStats ?? {}).filter((p) => p.Online).length;
}
function dt() {
  return new Date().toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false });
}

/* ── CIDR validation ───────────────────────────────────────────── */
function validateCIDR(cidr) {
  if (!cidr || !cidr.trim()) return { ok: true, value: "", error: "" };
  const m = cidr.trim().match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/);
  if (!m) return { ok: false, value: cidr, error: "格式无效，应为 x.x.x.x/n" };
  for (let i = 1; i <= 4; i++) { if (Number(m[i]) > 255) return { ok: false, value: cidr, error: "IP 地址超出范围" }; }
  const prefix = Number(m[5]);
  if (prefix < 1 || prefix > 32) return { ok: false, value: cidr, error: "前缀长度需在 1-32 之间" };
  return { ok: true, value: cidr.trim(), error: "" };
}

/* ── clipboard helper ──────────────────────────────────────────── */
async function copyText(text, btn) {
  try {
    await navigator.clipboard.writeText(text);
    if (btn) {
      const orig = btn.textContent;
      btn.textContent = "已复制";
      btn.disabled = true;
      setTimeout(() => { btn.textContent = orig; btn.disabled = false; }, 1500);
    }
    toast("已复制");
  } catch { toast("复制失败", "err"); }
}

/* ── fetch API helper ──────────────────────────────────────────── */
async function api(path, opts) {
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

/* ── refresh ───────────────────────────────────────────────────── */
async function refresh() {
  try {
    status = await api("/status");
    renderNetworks();
    renderStatus();
  } catch {
    status = null;
    renderNetworks();
    renderStatus();
  }
  maybeShowOnboarding();
  renderTabs();
}

/* ── tabs ──────────────────────────────────────────────────────── */
function renderTabs() {
  const nets = status?.networks ?? [];
  const pending = status?.pendingJoins ?? [];
  const cntNet = nets.length;
  const cntPending = pending.length;
  const netCnt = $('[data-tab="networks"] .cnt');
  const pendCnt = $('[data-tab="pending"] .cnt');
  if (netCnt) netCnt.textContent = cntNet ? "(" + cntNet + ")" : "";
  if (pendCnt) pendCnt.textContent = cntPending ? "(" + cntPending + ")" : "";
  if (cntPending) {
    const hotEl = $('[data-tab="pending"] .cnt');
    if (hotEl) hotEl.classList.add("hot");
  } else {
    const hotEl = $('[data-tab="pending"] .cnt');
    if (hotEl) hotEl.classList.remove("hot");
  }
}

function switchTab(id) {
  document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
  document.querySelectorAll(".tab-panel").forEach((p) => p.classList.remove("active"));
  const tabBtn = $('[data-tab="' + id + '"]');
  if (tabBtn) tabBtn.classList.add("active");
  const panel = $("#panel-" + id);
  if (panel) panel.classList.add("active");
}

/* ── render networks ───────────────────────────────────────────── */
function renderNetworks() {
  const list = $("#net-list");
  const nets = status?.networks ?? [];
  const pending = status?.pendingJoins ?? [];

  if (!nets.length && !pending.length) {
    list.innerHTML = `<div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M8 12h8M12 8v8"/></svg>
      <p class="t">未加入任何网络</p>
      <p class="s">点击「创建」或「加入」开始</p>
    </div>`;
    return;
  }

  let html = "";

  if (pending.length) {
    html += `<div class="card">
      <div class="card-head"><h3>待批准 · 加入请求</h3><button class="btn sm ghost" id="pending-clear-all">全部取消</button></div>
      <div id="pending-list">`;
    for (const p of pending) {
      const tid = esc(p.targetNid ?? "");
      html += `<div class="net pending">
        <div class="net-row">
          <div class="net-main">
            <div class="net-name"><span class="pill warn">待批准</span> <code>${esc(p.name || tid)}</code></div>
            <div class="net-meta"><span>网络 <code>${tid}</code></span></div>
          </div>
          <div class="net-side">
            <button class="btn sm danger ghost" data-cancel-pending="${esc(p.pendingId || p.targetNid)}">取消</button>
          </div>
        </div>
      </div>`;
    }
    html += `</div></div>`;
  }

  if (nets.length) {
    html += `<div class="net-list">`;
    for (const n of nets) {
      const on = !!n.interface;
      const stats = Object.values(n.peerStats ?? {});
      const bytes = stats.reduce((a, p) => a + (p.RxBytes ?? 0) + (p.TxBytes ?? 0), 0);
      const peers = stats.length;
      const linked = on;
      const owner = !!n.owner;
      const online = onlineCount(n);

      html += `<div class="net" data-nid="${esc(n.networkId)}">
        <div class="net-row">
          <div class="net-main">
            <div class="net-name">
              ${owner ? '<span class="pill owner">创建者</span>' : ""}
              <span>${esc(n.name || n.networkId)}</span>
            </div>
            <div class="net-meta">
              <span>网络 <code>${esc(n.networkId)}</code></span>
              ${n.ip ? `<span>IP <code>${esc(n.ip)}</code></span>` : ""}
              ${n.subnets?.length ? `<span>路由 ${n.subnets.map((s) => `<code>${esc(s)}</code>`).join(", ")}</span>` : ""}
            </div>
            <div class="net-stats">
              <span>收/发 <b>${fmtBytes(bytes)}</b></span>
              <span>成员 <b>${online}/${peers}</b> 在线</span>
              <span>隧道 <code>${esc(n.interface || "无")}</code></span>
              ${linked ? '<span class="pill ok">已链接</span>' : '<span class="pill off">未链接</span>'}
            </div>
          </div>
          <div class="net-side">
            <label class="switch"><input type="checkbox" ${on ? "checked" : ""} data-toggle-link="${esc(n.networkId)}" /><span class="slider"></span></label>
          </div>
        </div>
        <div class="net-actions">
          ${owner ? `
            <button class="btn sm ghost" data-detail="${esc(n.networkId)}">详情</button>
            <button class="btn sm ghost" data-members="${esc(n.networkId)}">成员</button>
            <button class="btn sm ghost" data-subnets="${esc(n.networkId)}">子网</button>
            <button class="btn sm ghost" data-invite="${esc(n.networkId)}">邀请</button>
            <button class="btn sm ghost" data-reset-code="${esc(n.networkId)}">查看配对码</button>
            <button class="btn sm danger" data-delete="${esc(n.networkId)}">删除</button>
          ` : `
            <button class="btn sm ghost" data-rejoin="${esc(n.networkId)}">重新连接</button>
            <button class="btn sm danger ghost" data-leave="${esc(n.networkId)}">退出网络</button>
          `}
        </div>
      </div>`;
    }
    html += `</div>`;
  }

  list.innerHTML = html;

  // bind events
  list.querySelectorAll("[data-toggle-link]").forEach((sw) => {
    sw.addEventListener("change", async (e) => {
      const nid = e.currentTarget.dataset.toggleLink;
      try {
        if (e.currentTarget.checked) {
          await api("/rejoin", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) });
        } else {
          await api("/leave", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) });
        }
        await refresh();
      } catch (err) { toast(String(err), "err"); await refresh(); }
    });
  });

  list.querySelectorAll("[data-detail]").forEach((b) => b.addEventListener("click", (e) => openDetailModal(e.currentTarget.dataset.detail)));
  list.querySelectorAll("[data-members]").forEach((b) => b.addEventListener("click", (e) => openMembersModal(e.currentTarget.dataset.members)));
  list.querySelectorAll("[data-subnets]").forEach((b) => b.addEventListener("click", (e) => openSubnetsModal(e.currentTarget.dataset.subnets)));
  list.querySelectorAll("[data-invite]").forEach((b) => b.addEventListener("click", (e) => openInviteModal(e.currentTarget.dataset.invite)));
  list.querySelectorAll("[data-reset-code]").forEach((b) => b.addEventListener("click", (e) => resetCode(e.currentTarget.dataset.resetCode)));
  list.querySelectorAll("[data-delete]").forEach((b) => b.addEventListener("click", (e) => deleteNetwork(e.currentTarget.dataset.delete)));
  list.querySelectorAll("[data-rejoin]").forEach((b) => b.addEventListener("click", (e) => rejoinNetwork(e.currentTarget.dataset.rejoin)));
  list.querySelectorAll("[data-leave]").forEach((b) => b.addEventListener("click", (e) => leaveNetwork(e.currentTarget.dataset.leave)));
  list.querySelectorAll("[data-cancel-pending]").forEach((b) => b.addEventListener("click", (e) => cancelPending(e.currentTarget.dataset.cancelPending)));
  const clearAllBtn = $("#pending-clear-all");
  if (clearAllBtn) clearAllBtn.addEventListener("click", async () => {
    const pends = status?.pendingJoins ?? [];
    for (const p of pends) { try { await cancelPending(p.pendingId || p.targetNid); } catch {} }
    await refresh();
  });
}

/* ── render status ─────────────────────────────────────────────── */
function renderStatus() {
  if (!status) {
    $("#device-id").textContent = "-";
    $("#svc-server").textContent = "-";
    $("#svc-wgport").textContent = "-";
    $("#tunnel-detail").innerHTML = `<p class="muted">连接失败</p>`;
    $("#status-json").textContent = "(未连接)";
    return;
  }
  $("#device-id").textContent = status.deviceId ?? "-";
  const addr = status.serverAddr ?? "";
  $("#svc-server").textContent = status.bound && addr ? addr : "未连接服务器";
  $("#svc-wgport").textContent = String(status.wgPort ?? "-");
  const nets = status.networks ?? [];
  $("#tunnel-detail").innerHTML = nets.length
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
  $("#status-json").textContent = JSON.stringify(status, null, 2);
}

/* ── create modal ──────────────────────────────────────────────── */
async function openCreateModal() {
  const s = status;
  const bound = !!s?.bound;
  const serverAddr = s?.serverAddr ?? "";
  const hasOwner = (s?.networks ?? []).some((n) => n.owner);
  const serverHint = bound && serverAddr
    ? `<p class="hint">将在已连接的服务器上创建：<code>${esc(serverAddr)}</code></p>`
    : `<p class="msg">未连接服务器：请先在「设置」中绑定服务器。</p>`;

  // fetch local subnets for suggestions
  let localSubnets = [];
  try { const resp = await api("/local-subnets"); localSubnets = resp?.subnets ?? (Array.isArray(resp) ? resp : []); } catch {}

  openModal({
    title: "创建网络",
    body: `${hasOwner ? '<p class="msg warn">本设备已创建网络（每客户端仅能创建一个）。如需新网络，请先删除或退出当前网络。</p>' : ""}
      <div class="row"><label>网络名称</label><input id="m-name" type="text" placeholder="例如：家庭网络" /></div>
      <div class="row"><label>网段</label><input id="m-subnet" type="text" placeholder="留空自动分配（如 10.88.0.0/24）" /></div>
      ${localSubnets.length ? '<div class="subnet-suggestions" id="m-subnet-suggestions"><span class="hint">本机已占用网段：</span>' + localSubnets.map(s => '<code class="chip" style="cursor:pointer" data-subnet="' + esc(s) + '">' + esc(s) + '</code>').join(" ") + '</div>' : ""}
      <div class="row"><label>WireGuard 端口</label><input id="m-port" type="number" min="1024" max="65535" value="51820" /></div>
      <div class="row"><label>CA 证书路径</label><input id="m-ca" type="text" placeholder="公共证书留空；自签名服务器填路径" /></div>
      <div class="row"><label>需批准才能加入</label><input id="m-approval" type="checkbox" /></div>
      ${serverHint}
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn" ${hasOwner || !bound ? "disabled" : ""}>创建</button>`,
    onBody: (body) => {
      const submit = body.querySelector("#m-submit");
      const result = body.querySelector("#m-result");
      // subnet suggestion click
      body.querySelectorAll("[data-subnet]").forEach((chip) => {
        chip.addEventListener("click", () => { body.querySelector("#m-subnet").value = chip.dataset.subnet; });
      });
      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = SPIN + " 创建中…";
        result.className = "msg";
        result.textContent = "";
        try {
          const name = body.querySelector("#m-name").value.trim();
          const subnetVal = body.querySelector("#m-subnet").value.trim();
          const subnetChk = validateCIDR(subnetVal);
          if (!subnetChk.ok) throw new Error("网段无效：" + subnetChk.error);
          const subnet = subnetChk.value;
          const port = Number(body.querySelector("#m-port").value);
          const ca = body.querySelector("#m-ca").value.trim();
          const approval = body.querySelector("#m-approval").checked;
          if (!name) throw new Error("请输入网络名称");
          if (!bound) throw new Error("未连接服务器：请先在「设置」中绑定服务器");
          const r = await api("/create", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name, subnet, port, ca, approvalRequired: approval }),
          });
          result.className = "msg ok";
          result.textContent = "已创建: " + (r.networkId ?? "");
          openInviteModal(r.networkId, r.pairingCode);
          await refresh();
        } catch (e) {
          result.className = "msg";
          result.textContent = "创建失败: " + e;
          submit.disabled = false;
          submit.innerHTML = "创建";
        }
      });
    },
  });
}

/* ── join modal ────────────────────────────────────────────────── */
async function openJoinModal() {
  const s = status;
  const bound = !!s?.bound;
  const serverAddr = s?.serverAddr ?? "";
  const serverHint = bound && serverAddr
    ? `<p class="hint">将加入已连接服务器上的网络：<code>${esc(serverAddr)}</code></p>`
    : `<p class="msg">未连接服务器：请先在「设置」中绑定服务器；或粘贴带有服务器地址的邀请链接后加入。</p>`;

  openModal({
    title: "加入网络",
    body: `<div class="row"><label>邀请链接</label><input id="m-link" type="text" placeholder="snet://join?nid=...&code=..." /></div>
      <p class="hint" style="text-align:center">或手动输入</p>
      <div class="row"><label>网络ID</label><input id="m-nid" type="text" placeholder="6 位网络ID" /></div>
      <div class="row"><label>配对码</label><input id="m-code" type="text" placeholder="12 位配对码" /></div>
      <div class="row"><label>WireGuard 端口</label><input id="m-port" type="number" min="1024" max="65535" value="51820" /></div>
      <div class="row"><label>CA 证书路径</label><input id="m-ca" type="text" placeholder="公共证书留空；自签名服务器填路径" /></div>
      ${serverHint}
      <div id="m-bind-auth" hidden></div>
      <p class="msg" id="m-result"></p>`,
    footer: `<button data-close class="btn ghost">取消</button><button id="m-submit" class="btn">加入</button>`,
    onBody: (body) => {
      const submit = body.querySelector("#m-submit");
      const result = body.querySelector("#m-result");
      const authWrap = body.querySelector("#m-bind-auth");
      const caInput = body.querySelector("#m-ca");

      // bind-then-join flow
      const bindAndThen = (server, then) => {
        authWrap.hidden = false;
        authWrap.innerHTML = `
          <div class="settings-block">
            <p class="msg">该邀请属于服务器 <code>${esc(server)}</code>，本机尚未绑定该服务器。请输入设备授权码以绑定后加入：</p>
            <div class="row"><label>设备授权码</label><input id="m-bind-code" type="text" placeholder="管理端生成的授权码" autocomplete="off" /></div>
            <div class="settings-actions"><button id="m-bind-go" class="btn">绑定并加入</button></div>
            <p class="msg" id="m-bind-result"></p>
          </div>`;
        const codeInput = authWrap.querySelector("#m-bind-code");
        const bindResult = authWrap.querySelector("#m-bind-result");
        const bindBtn = authWrap.querySelector("#m-bind-go");
        codeInput.focus();
        const go = async () => {
          const code = codeInput.value.trim();
          if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
          bindBtn.disabled = true;
          bindResult.className = "msg"; bindResult.textContent = "正在绑定…";
          try {
            const ca = caInput.value.trim();
            await api("/bind", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ server, ca, code }) });
            bindResult.className = "msg ok"; bindResult.textContent = "已绑定 " + server + "，正在加入…";
            await then();
          } catch (e) {
            bindResult.className = "msg"; bindResult.textContent = "绑定失败: " + e;
            bindBtn.disabled = false;
          }
        };
        bindBtn.addEventListener("click", go);
        codeInput.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
      };

      submit.addEventListener("click", async () => {
        submit.disabled = true;
        submit.innerHTML = SPIN + " 加入中…";
        result.className = "msg";
        result.textContent = "";
        try {
          const port = Number(body.querySelector("#m-port").value);
          const ca = caInput.value.trim();
          let link = body.querySelector("#m-link").value.trim();
          if (!link) {
            const nid = body.querySelector("#m-nid").value.trim();
            const code = body.querySelector("#m-code").value.trim();
            if (!nid || !code) throw new Error("请输入邀请链接，或网络ID + 配对码");
            link = "snet://join?nid=" + nid + "&code=" + code;
          }
          // parse link server
          let linkServer = "";
          try { const u = new URL(link); linkServer = u.searchParams.get("server") || ""; } catch {}
          const srv = status?.serverAddr ?? "";
          const normalizeSrv = (s) => s.replace(/\/+$/, "");

          class NeedBind extends Error {}
          const doJoin = async () => {
            const server = linkServer || srv;
            if (!server) throw new Error("未连接服务器：请先在「设置」中绑定服务器");
            try {
              const r = await api("/join", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ link, port, ca }),
              });
              result.className = "msg ok";
              if (r.status === "pending") {
                result.innerHTML = '<p>已提交加入请求，等待网络创建者批准。</p><p class="hint">批准后本机会自动加入并连接；也可在上方「待批准 · 加入请求」卡片中取消。</p>';
              } else {
                result.textContent = "已加入: IP " + (r.ip ?? "-") + "，网络 " + (r.networkId ?? "-");
              }
              await refresh();
              setTimeout(closeModal, 600);
            } catch (e) {
              if (String(e).includes("设备未授权")) throw new NeedBind();
              throw e;
            }
          };

          if (linkServer && srv && normalizeSrv(linkServer) === normalizeSrv(srv)) {
            await doJoin();
          } else if (linkServer) {
            try { await doJoin(); }
            catch (e) {
              if (e instanceof NeedBind) {
                await new Promise((resolve, reject) => {
                  bindAndThen(linkServer, async () => {
                    try { await doJoin(); resolve(); }
                    catch (e2) { result.className = "msg"; result.textContent = "加入失败: " + e2; reject(e2); }
                  });
                });
              } else { throw e; }
            }
          } else {
            await doJoin();
          }
        } catch (e) {
          result.className = "msg";
          result.textContent = "加入失败: " + e;
          submit.disabled = false;
          submit.innerHTML = "加入";
        }
      });
    },
  });
}

/* ── invite modal ──────────────────────────────────────────────── */
function openInviteModal(nid, pairingCode) {
  const n = (status?.networks ?? []).find((x) => x.networkId === nid);
  const code = n?.pairingCode ?? pairingCode ?? "";
  const link = n?.inviteLink || ("snet://join?nid=" + nid + "&code=" + code);
  openModal({
    title: "邀请链接",
    body: `<div id="m-qr" class="qr" style="text-align:center"></div>
      <div class="row"><label>邀请链接</label><input id="m-inv-link" type="text" value="${esc(link)}" readonly /></div>
      <p class="hint">成员扫描二维码或粘贴链接即可加入</p>
      <p class="hint">加入者IP将由服务器在网段内自动分配。</p>`,
    footer: `<button id="m-copy" class="btn">复制邀请链接</button><button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      renderQR(body.querySelector("#m-qr"), link);
      body.querySelector("#m-copy").addEventListener("click", (e) => {
        copyText(link, e.currentTarget);
      });
    },
  });
}

/* ── detail modal ──────────────────────────────────────────────── */
async function openDetailModal(nid) {
  let detail;
  try { detail = await api("/netinfo?nid=" + encodeURIComponent(nid)); } catch (e) { toast("加载失败: " + e, "err"); return; }
  openModal({
    title: "网络详情",
    wide: true,
    body: `<div style="font-size:12px;color:var(--dim)"><pre class="json">${esc(JSON.stringify(detail, null, 2))}</pre></div>`,
    footer: `<button data-close class="btn ghost">关闭</button>`,
  });
}

/* ── members modal ─────────────────────────────────────────────── */
async function openMembersModal(nid) {
  let peersResp;
  try { peersResp = await api("/peers?nid=" + encodeURIComponent(nid)); } catch (e) { toast("加载失败: " + e, "err"); return; }
  const selfNode = peersResp.self;
  const peers = peersResp.peers ?? [];
  const all = selfNode ? [selfNode, ...peers] : peers;
  const n = (status?.networks ?? []).find((x) => x.networkId === nid);
  const isOwner = !!n?.owner;
  let rows = "";
  for (const nd of all) {
    const isSelf = selfNode && nd.ip === selfNode.ip;
    const online = nd.online ? '<span class="pill ok">在线</span>' : '<span class="pill off">离线</span>';
    const subnets = (nd.allowedSubnets?.length ?? 0) > 0
      ? '<span class="pill subnet-route">' + esc(nd.allowedSubnets.join(", ")) + "</span>"
      : '<span class="muted">-</span>';
    rows += `<tr>
      <td class="mono">${esc(nd.ip)}</td>
      <td>${nd.deviceId ? '<span class="muted mono">' + esc(nd.deviceId.slice(0, 8)) + "</span>" : "-"}</td>
      <td>${online}</td>
      <td>${subnets}</td>
      <td>${isSelf ? '<span class="muted">自己</span>' : isOwner ? '<button class="btn sm danger ghost" data-kick="' + esc(nd.ip) + '">踢出</button>' : ""}</td>
    </tr>`;
  }
  openModal({
    title: "成员 · " + nid,
    wide: true,
    body: (isOwner ? "" : '<p class="hint">成员列表（只读，本机非创建者）</p>') +
      '<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>设备</th><th>状态</th><th>子网路由</th><th></th></tr></thead><tbody>' +
      (rows || '<tr><td colspan="5" style="text-align:center;color:var(--dim)">暂无成员</td></tr>') +
      "</tbody></table></div>",
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      if (!isOwner) return;
      body.querySelectorAll("[data-kick]").forEach((b) => b.addEventListener("click", async (e) => {
        const ip = e.currentTarget.dataset.kick;
        if (!(await confirmDialog("踢出成员", "踢出该成员？该设备将立即断开。", true))) return;
        try {
          await api("/kick", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid, nodeId: ip }) });
          toast("已踢出");
          closeModal();
          await refresh();
        } catch (err) { toast("操作失败: " + err, "err"); }
      }));
    },
  });
}

/* ── subnets modal ─────────────────────────────────────────────── */
async function openSubnetsModal(nid) {
  let detail;
  try { detail = await api("/netinfo?nid=" + encodeURIComponent(nid)); } catch (e) { toast("加载失败: " + e, "err"); return; }
  const subnets = detail.subnets ?? [];
  let tagsHtml = subnets.map((s) => `<span class="chip" data-del-subnet="${esc(s)}">${esc(s)} <button class="chip-del">&times;</button></span>`).join("");
  openModal({
    title: "子网路由",
    body: `<p class="hint">为该网络添加子网路由，其他成员可通过你的隧道访问这些网段。</p>
      <div class="subnet-add-row" style="margin-top:10px"><input id="m-add-subnet" type="text" placeholder="如 192.168.1.0/24" /><button id="m-add-subnet-btn" class="btn sm">添加</button></div>
      <div class="subnet-tags" id="m-subnet-tags">${tagsHtml || '<span class="hint">暂无子网</span>'}</div>
      <p class="msg" id="m-subnet-result"></p>`,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const tagsWrap = body.querySelector("#m-subnet-tags");
      const resultEl = body.querySelector("#m-subnet-result");

      async function saveSubnets(newList) {
        try {
          await api("/subnets", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid, subnets: newList }) });
          subnets.length = 0;
          for (const s of newList) subnets.push(s);
          resultEl.className = "msg ok"; resultEl.textContent = "已保存";
          tagsWrap.innerHTML = newList.map((s) => `<span class="chip" data-del-subnet="${esc(s)}">${esc(s)} <button class="chip-del">&times;</button></span>`).join("") || '<span class="hint">暂无子网</span>';
          bindDel();
          await refresh();
        } catch (err) { resultEl.className = "msg"; resultEl.textContent = "保存失败: " + err; }
      }

      body.querySelector("#m-add-subnet-btn").addEventListener("click", () => {
        const val = body.querySelector("#m-add-subnet").value.trim();
        if (!val) return;
        const chk = validateCIDR(val);
        if (!chk.ok) { resultEl.className = "msg"; resultEl.textContent = "网段无效：" + chk.error; return; }
        const cur = [...subnets];
        if (cur.includes(chk.value)) { resultEl.className = "msg"; resultEl.textContent = "已存在"; return; }
        cur.push(chk.value);
        saveSubnets(cur);
        body.querySelector("#m-add-subnet").value = "";
      });

      function bindDel() {
        tagsWrap.querySelectorAll("[data-del-subnet]").forEach((ch) => {
          ch.querySelector(".chip-del").addEventListener("click", () => {
            const val = ch.dataset.delSubnet;
            saveSubnets(subnets.filter((s) => s !== val));
          });
        });
      }
      bindDel();
    },
  });
}

/* ── action helpers ────────────────────────────────────────────── */
async function deleteNetwork(nid) {
  if (!(await confirmDialog("删除网络", "确定删除该网络？所有成员将断开，此操作不可恢复。", true))) return;
  try { await api("/delete", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) }); toast("已删除"); await refresh(); }
  catch (e) { toast("删除失败: " + e, "err"); }
}
async function rejoinNetwork(nid) {
  try { await api("/rejoin", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) }); toast("已重新连接"); await refresh(); }
  catch (e) { toast("操作失败: " + e, "err"); }
}
async function leaveNetwork(nid) {
  if (!(await confirmDialog("退出网络", "确定退出该网络？配置将被移除，需重新扫码加入。", true))) return;
  try { await api("/remove", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) }); toast("已退出"); await refresh(); }
  catch (e) { toast("操作失败: " + e, "err"); }
}
async function cancelPending(pendingId) {
  try { await api("/cancel-pending", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ pendingId }) }); toast("已取消"); }
  catch (e) { toast("取消失败: " + e, "err"); }
}
async function resetCode(nid) {
  if (!(await confirmDialog("重置配对码", "作废旧码并生成新码？旧码立即失效，已加入成员不受影响。", true))) return;
  try {
    const r = await api("/reset-code", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nid }) });
    toast("新配对码: " + (r.code ?? ""));
  } catch (e) { toast("操作失败: " + e, "err"); }
}

/* ── settings modal ────────────────────────────────────────────── */
const HELP_ROWS = [
  ["链接开关", "全部", "开启＝连接该网络隧道；关闭＝断开本机该网络，保留配置与服务器节点，可随时再开"],
  ["详情", "创建者", "查看服务器上该网络的完整信息（成员、中继端口、在线状态、创建时间等）"],
  ["成员", "创建者", "查看成员列表与在线状态；批准/拒绝待批准加入请求、踢出成员"],
  ["子网", "创建者", "管理本节点向该网络宣告的子网路由"],
  ["邀请", "创建者", "查看邀请链接与二维码，复制后分享给其他设备"],
  ["查看配对码", "创建者", "查看当前配对码并复制；可作废旧码并生成新码，旧码立即失效、已加入成员不受影响"],
  ["删除", "创建者", "彻底删除该网络：所有成员断开、网段释放，不可恢复"],
  ["退出网络", "成员", "本机移出该网络并遗忘配置，需重新扫码加入；创建者无此按钮"],
];

function openSettingsModal() {
  const s = status;
  const bound = !!s?.bound;
  const serverAddr = s?.serverAddr ?? "";
  const helpRows = HELP_ROWS.map(([op, who, desc]) => `<tr><td>${op}</td><td>${who}</td><td>${desc}</td></tr>`).join("");

  openModal({
    title: "设置",
    body: `<div class="settings-block">
        <div class="row"><label>设备ID</label><input type="text" value="${esc(s?.deviceId ?? "-")}" readonly /></div>
        <div class="row"><label>服务器地址</label><input id="s-server" type="text" value="${esc(serverAddr)}" placeholder="https://example.com:8090" /></div>
        <div class="row"><label>设备授权码</label><input id="s-code" type="text" placeholder="管理端生成的设备授权码（仅用于绑定，不保存）" autocomplete="off" /></div>
        <div class="row"><label>CA 证书路径</label><input id="s-ca" type="text" placeholder="公共证书留空；自签名服务器填路径" /></div>
        <div class="settings-actions">
          <button id="s-bind" class="btn">${bound ? "重新绑定" : "绑定服务器"}</button>
        </div>
        <p class="msg" id="s-bind-result"></p>
      </div>
      <div class="row"><label>WireGuard 端口</label><input id="s-wgport" type="number" min="1024" max="65535" value="${s?.wgPort ?? 51820}" /></div>
      <p class="msg" id="s-msg"></p>
      <details class="help">
        <summary>操作说明</summary>
        <div class="tbl-wrap"><table class="help">
          <thead><tr><th>操作</th><th>适用</th><th>作用</th></tr></thead>
          <tbody>${helpRows}</tbody>
        </table></div>
      </details>`,
    wide: true,
    footer: `<button data-close class="btn ghost">关闭</button>`,
    onBody: (body) => {
      const bindResult = body.querySelector("#s-bind-result");
      body.querySelector("#s-bind")?.addEventListener("click", async () => {
        const server = body.querySelector("#s-server").value.trim();
        const code = body.querySelector("#s-code").value.trim();
        const ca = body.querySelector("#s-ca").value.trim();
        if (!server) { bindResult.className = "msg"; bindResult.textContent = "请输入服务器地址"; return; }
        if (!code) { bindResult.className = "msg"; bindResult.textContent = "请输入设备授权码"; return; }
        bindResult.className = "msg"; bindResult.textContent = "正在绑定…";
        try {
          await api("/bind", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ server, code, ca }) });
          bindResult.className = "msg ok"; bindResult.textContent = "已绑定 " + server;
          toast("已绑定服务器");
          await refresh();
        } catch (e) { bindResult.className = "msg"; bindResult.textContent = "绑定失败: " + e; }
      });
    },
  });
}

/* ── onboarding ────────────────────────────────────────────────── */
let onboardingShown = false;
function maybeShowOnboarding() {
  if (onboardingShown) return;
  if (status && status.bound) return;
  if (status && (status.networks?.length || status.pendingJoins?.length)) return;
  onboardingShown = true;
  renderOnboarding();
}
function renderOnboarding() {
  const ov = $("#onboarding");
  ov.hidden = false;
}
async function finishOnboarding() {
  $("#onboarding").hidden = true;
  await refresh();
}

/* ── event bindings ────────────────────────────────────────────── */
document.addEventListener("DOMContentLoaded", () => {
  // tabs
  document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", (e) => switchTab(e.currentTarget.dataset.tab)));

  // header buttons
  $("#btn-settings").addEventListener("click", openSettingsModal);
  $("#btn-create").addEventListener("click", openCreateModal);
  $("#btn-join").addEventListener("click", openJoinModal);
  $("#btn-refresh").addEventListener("click", () => void refresh());

  // onboarding bind
  const onbBind = $("#onb-bind");
  if (onbBind) onbBind.addEventListener("click", async () => {
    const server = $("#onb-server").value.trim();
    const code = $("#onb-code").value.trim();
    const result = $("#onb-bind-result");
    if (!server || !code) { result.className = "msg"; result.textContent = "请填写服务器地址和授权码"; return; }
    result.className = "msg"; result.textContent = "正在绑定…";
    try {
      await api("/bind", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ server, code }) });
      result.className = "msg ok"; result.textContent = "已绑定 " + server;
      await finishOnboarding();
    } catch (e) { result.className = "msg"; result.textContent = "绑定失败: " + e; }
  });

  const onbSkip = $("#onb-skip");
  if (onbSkip) onbSkip.addEventListener("click", (e) => { e.preventDefault(); finishOnboarding(); });

  // initial load
  refresh();
  setInterval(() => { if (!document.hidden) refresh(); }, 3000);
});
