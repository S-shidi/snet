(() => {
  var __create = Object.create;
  var __defProp = Object.defineProperty;
  var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
  var __getOwnPropNames = Object.getOwnPropertyNames;
  var __getProtoOf = Object.getPrototypeOf;
  var __hasOwnProp = Object.prototype.hasOwnProperty;
  var __require = /* @__PURE__ */ ((x) => typeof require !== "undefined" ? require : typeof Proxy !== "undefined" ? new Proxy(x, {
    get: (a, b) => (typeof require !== "undefined" ? require : a)[b]
  }) : x)(function(x) {
    if (typeof require !== "undefined") return require.apply(this, arguments);
    throw Error('Dynamic require of "' + x + '" is not supported');
  });
  var __copyProps = (to, from, except, desc) => {
    if (from && typeof from === "object" || typeof from === "function") {
      for (let key of __getOwnPropNames(from))
        if (!__hasOwnProp.call(to, key) && key !== except)
          __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
    }
    return to;
  };
  var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
    // If the importer is in node compatibility mode or this is not an ESM
    // file that has been converted to a CommonJS file using a Babel-
    // compatible transform (i.e. "__esModule" has not been set), then set
    // "default" to the CommonJS "module.exports" for node compatibility.
    isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
    mod
  ));

  // shared/web/utils.ts
  var $ = (sel) => document.querySelector(sel);
  var esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  var SPIN = '<span class="spinner"></span>';
  var qrcodeFn = null;
  async function loadQR() {
    if (qrcodeFn) return;
    try {
      const mod = await import("qrcode-generator");
      qrcodeFn = mod.default;
    } catch {
    }
  }
  function renderQR(container, text) {
    container.innerHTML = "";
    if (qrcodeFn) {
      const qr = qrcodeFn(0, "M");
      qr.addData(text);
      qr.make();
      const svg = qr.createSvgTag({ cellSize: 4, margin: 1, scalable: true });
      container.innerHTML = svg;
      container.querySelector("svg")?.setAttribute("style", "width:100%;height:auto;display:block");
    } else {
      container.innerHTML = `<p style="color:var(--dim);font-size:12px;text-align:center">${esc(text)}</p>`;
    }
  }
  function fmtBytes(b) {
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
  var ONLINE_WINDOW_SEC = 180;
  function nowSec() {
    return Math.floor(Date.now() / 1e3);
  }
  function onlineCount(n) {
    const stats = n.peerStats ?? {};
    const now = nowSec();
    return Object.values(stats).filter((p) => p.LastHandshakeSec && now - p.LastHandshakeSec < ONLINE_WINDOW_SEC).length;
  }
  function toast(msg, kind = "ok") {
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
  async function copyText(text, btn) {
    try {
      await navigator.clipboard.writeText(text);
      toast("\u5DF2\u590D\u5236\u5230\u526A\u8D34\u677F");
    } catch {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy");
        toast("\u5DF2\u590D\u5236\u5230\u526A\u8D34\u677F");
      } catch {
        toast("\u590D\u5236\u5931\u8D25\uFF0C\u8BF7\u624B\u52A8\u590D\u5236", "err");
      }
      ta.remove();
    }
    if (btn) {
      const old = btn.innerHTML;
      btn.innerHTML = '<span style="color:var(--ok)">\u5DF2\u590D\u5236</span>';
      setTimeout(() => {
        btn.innerHTML = old;
      }, 1400);
    }
  }
  var modalRoot = $("#modal-root");
  var modalOnClose = null;
  function openModal(opts) {
    modalRoot.innerHTML = `
    <div class="modal-backdrop">
      <div class="modal${opts.wide ? " wide" : ""}" role="dialog" aria-modal="true">
        <div class="modal-head">
          <h3>${esc(opts.title)}</h3>
          <button data-close class="btn icon" title="\u5173\u95ED">\u2715</button>
        </div>
        <div class="modal-body">${opts.body}</div>
        <div class="modal-foot">
          ${opts.footer ?? ""}
          ${opts.onSave ? `<button id="m-save" class="btn">\u4FDD\u5B58</button>` : ""}
        </div>
      </div>
    </div>`;
    modalRoot.hidden = false;
    modalOnClose = opts.onClose ?? null;
    const el = modalRoot.querySelector(".modal");
    const backdrop = modalRoot.querySelector(".modal-backdrop");
    backdrop.addEventListener("mousedown", (e) => {
      if (e.target === backdrop) closeModal();
    });
    modalRoot.querySelectorAll("[data-close]").forEach((b) => b.addEventListener("click", closeModal));
    const saveBtn = modalRoot.querySelector("#m-save");
    saveBtn?.addEventListener("click", () => {
      opts.onSave?.(el);
      closeModal();
    });
    try {
      opts.onBody?.(el);
    } catch (err) {
      console.error("modal onBody:", err);
    }
    const f = el.querySelector("input, select, textarea");
    if (f) setTimeout(() => f.focus(), 30);
  }
  function closeModal() {
    const cb = modalOnClose;
    modalOnClose = null;
    modalRoot.hidden = true;
    modalRoot.innerHTML = "";
    cb?.();
  }
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeModal();
  });
  function confirmDialog(title, message, danger = false) {
    return new Promise((resolve) => {
      let settled = false;
      const done = (v) => {
        if (settled) return;
        settled = true;
        resolve(v);
        closeModal();
      };
      openModal({
        title,
        body: `<p class="confirm-msg">${esc(message)}</p>`,
        footer: `<button id="cd-no" class="btn ghost">\u53D6\u6D88</button><button id="cd-yes" class="${danger ? "btn danger" : "btn"}">\u786E\u5B9A</button>`,
        onClose: () => done(false),
        onBody: (m) => {
          m.querySelector("#cd-no").addEventListener("click", () => done(false));
          m.querySelector("#cd-yes").addEventListener("click", () => done(true));
        }
      });
    });
  }
  function initTabs() {
    const tabs = Array.from(document.querySelectorAll(".tab"));
    const slider = document.getElementById("tabs-slider");
    function updateSlider() {
      const active = document.querySelector(".tab.active");
      if (!active || !slider) return;
      const bar = active.parentElement;
      const barRect = bar.getBoundingClientRect();
      const tabRect = active.getBoundingClientRect();
      const left = tabRect.left - barRect.left;
      slider.style.left = `${left}px`;
      slider.style.width = `${tabRect.width}px`;
    }
    tabs.forEach(
      (t) => t.addEventListener("click", () => {
        tabs.forEach((x) => x.classList.toggle("active", x === t));
        const name = t.dataset.tab;
        document.querySelectorAll(".tab-panel").forEach(
          (p) => p.classList.toggle("active", p.id === `tab-${name}` || p.id === `panel-${name}`)
        );
        updateSlider();
      })
    );
    requestAnimationFrame(updateSlider);
    window.addEventListener("resize", updateSlider);
  }

  // shared/web/subnet.ts
  var SUBNET_PREFIXES = [24, 23, 22, 20, 16, 12, 8];
  var SUBNET_CHIPS = ["10.88.0.0/24", "10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"];
  function subnetIsPrivate(u32) {
    if (u32 >>> 24 === 10) return true;
    if (u32 >= 2886729728 && u32 <= 2887778303) return true;
    if (u32 >= 3232235520 && u32 <= 3232301055) return true;
    return false;
  }
  function subnetBaseU32(u32, prefix) {
    const mask = ~0 << 32 - prefix >>> 0;
    return (u32 & mask) >>> 0;
  }
  function u32ToIp(u32) {
    return `${u32 >>> 24}.${u32 >>> 16 & 255}.${u32 >>> 8 & 255}.${u32 & 255}`;
  }
  function subnetCheck(raw, selectPrefix) {
    const s = raw.trim();
    if (!s) return { ok: true, value: "", text: "" };
    let host = s;
    let prefix = selectPrefix;
    const slash = s.indexOf("/");
    if (slash >= 0) {
      host = s.slice(0, slash);
      const p = Number(s.slice(slash + 1).trim());
      if (!Number.isInteger(p) || p < 8 || p > 24) {
        return { ok: false, error: "\u524D\u7F00\u9700\u4E3A /8\u2013/24" };
      }
      prefix = p;
    }
    const parts = host.split(".");
    if (parts.length !== 4) return { ok: false, error: "\u683C\u5F0F\u65E0\u6548\uFF0C\u5982 10.88.0.0/24" };
    const oct = parts.map((o) => {
      if (!/^\d{1,3}$/.test(o)) return NaN;
      return Number(o);
    });
    if (oct.some((o) => Number.isNaN(o) || o < 0 || o > 255)) {
      return { ok: false, error: "IPv4 \u5730\u5740\u6BB5\u9700\u5728 0\u2013255" };
    }
    const u32 = (oct[0] << 24 | oct[1] << 16 | oct[2] << 8 | oct[3]) >>> 0;
    const base = subnetBaseU32(u32, prefix);
    if (!subnetIsPrivate(base)) {
      return { ok: false, error: "\u4EC5\u652F\u6301\u79C1\u6709\u5185\u7F51\u6BB5\uFF1A10/8\u3001172.16/12\u3001192.168/16" };
    }
    const value = `${u32ToIp(base)}/${prefix}`;
    if (base !== u32) {
      return { ok: true, value, fix: true, text: `\u4E3B\u673A\u4F4D\u5E94\u4E3A 0\uFF0C\u5DF2\u4FEE\u6B63\u4E3A ${value}` };
    }
    const capacity = Math.max(0, 2 ** (32 - prefix) - 2).toLocaleString("en-US");
    return { ok: true, value, text: `\u2713 ${value} \xB7 \u79C1\u6709\u7F51\u6BB5\uFF0C\u53EF\u5BB9\u7EB3 ${capacity} \u53F0` };
  }
  function subnetWidgetHTML(opts) {
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
        <input id="${id}" class="subnet-base" type="text" value="${esc(base)}" placeholder="${esc(opts.placeholder || "10.88.0.0/24\uFF08\u7559\u7A7A\u81EA\u52A8\u5206\u914D\uFF09")}" spellcheck="false" autocomplete="off" />
        <select class="subnet-prefix" aria-label="\u524D\u7F00">${prefixOpts}</select>
      </div>
      <div class="subnet-chips">${chips}</div>
      <p class="subnet-status"></p>
    </div>`;
  }
  function subnetWidgetInit(wrap) {
    const input = wrap.querySelector(".subnet-base");
    const sel = wrap.querySelector(".subnet-prefix");
    const status2 = wrap.querySelector(".subnet-status");
    const emptyHint = wrap.dataset.empty || "";
    const render = () => {
      const s = input.value.trim();
      if (!s) {
        status2.className = "subnet-status";
        status2.textContent = emptyHint;
        return;
      }
      const chk = subnetCheck(s, Number(sel.value));
      if (!chk.ok) {
        status2.className = "subnet-status err";
        status2.textContent = chk.error;
        return;
      }
      status2.className = chk.fix ? "subnet-status warn" : "subnet-status ok";
      status2.textContent = chk.text;
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
    wrap.querySelectorAll(".subnet-chips .chip").forEach((c) => {
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
      render
    };
  }

  // shared/web/ui.ts
  var backend;
  var status = null;
  var onboardingShown = false;
  var skipOnboarding = false;
  var SETTINGS_KEY = "snet.settings";
  var LEGACY_CA = "/usr/local/snet/certs/server.pem";
  var DEFAULT_SETTINGS = { server: "https://snet.uizhi.eu.org:8090", ca: "", wgport: 51820 };
  function loadSettings() {
    let s;
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
  function saveSettings(s) {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  }
  function currentServer() {
    if (status?.bound) return status.serverAddr ?? "";
    return "";
  }
  async function refresh() {
    const btn = $("#btn-refresh");
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
    if (btn) setTimeout(() => {
      btn.disabled = false;
    }, 800);
  }
  var ownerInfo = {};
  var lastOwnerFetch = 0;
  async function refreshOwnerInfo(force = false) {
    const now = Date.now();
    if (!force && now - lastOwnerFetch < 1e4) return;
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
      })
    );
  }
  function renderHeader() {
    const dot = $("#daemon-state");
    if (!dot) return;
    if (backend.hasDaemonControl) {
      if (status) {
        dot.className = "live-dot ok";
        dot.textContent = "\u8FD0\u884C\u4E2D";
      } else {
        dot.className = "live-dot err";
        dot.textContent = "\u672A\u8FD0\u884C";
      }
      const btn = $("#header-svc-btn");
      if (btn) {
        btn.hidden = !!status;
        btn.disabled = false;
        if (!status) {
          btn.textContent = "\u542F\u52A8";
          btn.title = "\u542F\u52A8\u7CFB\u7EDF\u540E\u53F0\u5B88\u62A4\u8FDB\u7A0B\uFF08\u53EF\u80FD\u5F39\u51FA\u7BA1\u7406\u5458\u5BC6\u7801\u6846\uFF09";
          btn.onclick = () => void ensureDaemon();
        }
      }
    } else {
      dot.className = "live-dot ok";
      dot.textContent = "snetd";
    }
  }
  function netCard(n) {
    const linked = !!n.interface;
    const state = linked ? "\u5DF2\u94FE\u63A5" : n.active ? "\u672A\u5C31\u7EEA" : "\u672A\u94FE\u63A5";
    const cls = linked ? "ok" : n.active ? "warn" : "off";
    const peers = n.peerStats ?? {};
    const total = Object.keys(peers).length;
    const detail = n.owner ? ownerInfo[n.networkId] : void 0;
    const memberTotal = detail ? detail.nodes?.length ?? total + 1 : total + 1;
    const memberOnline = detail ? (detail.nodes ?? []).filter((nd) => nd.online).length : onlineCount(n) + (linked ? 1 : 0);
    const pendingCount = detail?.pendingCount ?? detail?.pending?.length ?? 0;
    const memberLine = memberTotal <= 1 ? `<span class="muted">\u6682\u65E0\u5176\u4ED6\u6210\u5458</span>` : `<span>\u6210\u5458 <b>${memberOnline}/${memberTotal}</b> \u5728\u7EBF</span>`;
    const tx = Object.values(peers).reduce((a, p) => a + (p.TxBytes ?? 0), 0);
    const rx = Object.values(peers).reduce((a, p) => a + (p.RxBytes ?? 0), 0);
    const ops = [];
    if (n.owner) {
      ops.push(`<button data-act="info" class="btn ghost sm" title="\u67E5\u770B\u670D\u52A1\u5668\u4E0A\u8BE5\u7F51\u7EDC\u7684\u5B8C\u6574\u4FE1\u606F">\u8BE6\u60C5</button>`);
      ops.push(`<button data-act="members" class="btn ghost sm" title="\u67E5\u770B\u6210\u5458\u5217\u8868\u4E0E\u5728\u7EBF\u72B6\u6001">\u6210\u5458 ${memberTotal}${pendingCount > 0 ? ` <span class="badge-dot" title="${pendingCount} \u4E2A\u5F85\u6279\u51C6\u8BF7\u6C42">${pendingCount}</span>` : ""}</button>`);
      ops.push(`<button data-act="invite" class="btn ghost sm" title="\u5C55\u793A\u9080\u8BF7\u94FE\u63A5\u4E0E\u52A0\u5165\u4E8C\u7EF4\u7801">\u9080\u8BF7</button>`);
      ops.push(`<button data-act="settings" class="btn ghost sm" title="\u4FEE\u6539\u7F51\u7EDC\u540D\u79F0\u3001\u7F51\u6BB5\u6216\u52A0\u5165\u6279\u51C6\u8BBE\u7F6E">\u8BBE\u7F6E</button>`);
      ops.push(`<button data-act="subnets" class="btn ghost sm" title="\u5BA3\u544A\u672C\u8BBE\u5907\u7684\u5C40\u57DF\u7F51\u5B50\u7F51">\u5B50\u7F51\u8DEF\u7531</button>`);
      ops.push(`<button data-act="code" class="btn ghost sm" title="\u67E5\u770B\u5F53\u524D\u914D\u5BF9\u7801\u5E76\u590D\u5236">\u67E5\u770B\u914D\u5BF9\u7801</button>`);
      ops.push(`<button data-act="delete" class="btn danger ghost sm" title="\u5F7B\u5E95\u5220\u9664\u7F51\u7EDC">\u5220\u9664</button>`);
    } else {
      ops.push(`<button data-act="subnets" class="btn ghost sm" title="\u5BA3\u544A\u672C\u8BBE\u5907\u7684\u5C40\u57DF\u7F51\u5B50\u7F51">\u5B50\u7F51\u8DEF\u7531</button>`);
      ops.push(`<button data-act="remove" class="btn danger ghost sm" title="\u672C\u673A\u9000\u51FA\u8BE5\u7F51\u7EDC\u5E76\u9057\u5FD8\u914D\u7F6E">\u9000\u51FA\u7F51\u7EDC</button>`);
    }
    const err = n.error ? `<p class="msg">${esc(n.error)}</p>` : "";
    return `
  <div class="net" data-nid="${esc(n.networkId)}">
    <div class="net-row">
      <div class="net-main">
        <div class="net-name">${esc(n.name || n.networkId)} ${n.owner ? `<span class="pill owner">owner</span>` : ""}</div>
        <div class="net-meta">
          <span>IP <code>${esc(n.ip ?? "-")}</code></span>
          <span>\u7F51\u6BB5 <code>${esc(n.subnet ?? "-")}</code></span>
          <span>ID <code>${esc(n.networkId)}</code></span>
          ${(n.allowedSubnets?.length ?? 0) > 0 ? `<span class="pill subnet-route">\u8DEF\u7531 ${n.allowedSubnets.length} \u4E2A\u5B50\u7F51</span>` : ""}
        </div>
        <div class="net-stats">${memberLine}<span>\u6536 <b>${fmtBytes(rx)}</b></span><span>\u53D1 <b>${fmtBytes(tx)}</b></span></div>
      </div>
      <div class="net-side">
        <span class="pill ${cls}">${state}</span>
        <label class="switch" title="${linked ? "\u65AD\u5F00\u8BE5\u7F51\u7EDC" : "\u8FDE\u63A5\u8BE5\u7F51\u7EDC"}">
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
      <div class="t">\u540E\u53F0\u670D\u52A1\u672A\u8FD0\u884C</div>
      <div class="s">\u9700\u8981\u7CFB\u7EDF\u540E\u53F0\u5B88\u62A4\u8FDB\u7A0B snetd \u7EF4\u6301\u7F51\u7EDC\u96A7\u9053</div>
      <div class="empty-actions">${backend.hasDaemonControl ? `<button id="empty-start" class="btn">\u542F\u52A8</button>` : ""}</div>
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
      <div class="t">\u8FD8\u6CA1\u6709\u52A0\u5165\u4EFB\u4F55\u7F51\u7EDC</div>
      <div class="s">\u521B\u5EFA\u4E00\u4E2A\u7F51\u7EDC\uFF0C\u6216\u4F7F\u7528\u9080\u8BF7\u94FE\u63A5/\u914D\u5BF9\u7801\u52A0\u5165\u5176\u4ED6\u8BBE\u5907</div>
      <div class="empty-actions">
        <button id="empty-create" class="btn">\u521B\u5EFA\u7F51\u7EDC</button>
        <button id="empty-join" class="btn ghost">\u52A0\u5165\u7F51\u7EDC</button>
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
    const pendingHTML = pending.length ? `<h4 class="pending-heading">\u5F85\u6279\u51C6 \xB7 \u52A0\u5165\u8BF7\u6C42</h4>` + pending.map(
      (p) => `<div class="net pending" data-pid="${esc(p.pendingId)}" data-nid="${esc(p.networkId)}">
        <div class="net-row">
          <div class="net-main">
            <div class="net-name">${esc(p.networkId)} <span class="pill warn">\u5F85\u6279\u51C6</span></div>
            <div class="net-meta">${p.error ? `<span class="muted">${esc(p.error)}</span>` : "\u7B49\u5F85\u7F51\u7EDC\u521B\u5EFA\u8005\u6279\u51C6\u52A0\u5165\u8BF7\u6C42"}</div>
          </div>
          <div class="net-side">
            <button data-act="cancel-pending" class="btn danger ghost sm">\u53D6\u6D88\u8BF7\u6C42</button>
          </div>
        </div>
      </div>`
    ).join("") : "";
    list.innerHTML = netHTML + pendingHTML;
    const cnt = $("#cnt-net");
    if (cnt) {
      cnt.textContent = String(nets.length);
      cnt.className = "cnt" + (pending.length ? " hot" : "");
      cnt.title = pending.length ? `${pending.length} \u4E2A\u5F85\u6279\u51C6\u8BF7\u6C42` : "";
    }
  }
  function bindNetListEvents() {
    const netList = $("#net-list");
    if (!netList) return;
    netList.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-act]");
      if (!btn) return;
      const card = btn.closest(".net");
      if (!card) return;
      if (btn.dataset.act === "cancel-pending") {
        if (!await confirmDialog("\u53D6\u6D88\u52A0\u5165\u8BF7\u6C42", "\u53D6\u6D88\u7B49\u5F85\u6279\u51C6\uFF1F", true)) return;
        try {
          await backend.cancelPending(card.dataset.pid);
          toast("\u5DF2\u53D6\u6D88\u52A0\u5165\u8BF7\u6C42");
        } catch (err) {
          toast(String(err), "err");
        }
        await refresh();
        return;
      }
      await onAction(btn.dataset.act, card.dataset.nid, btn);
    });
    netList.addEventListener("change", async (e) => {
      const input = e.target;
      if (input.dataset.act !== "toggle") return;
      const card = input.closest(".net");
      if (!card) return;
      await onToggle(card, input.checked);
    });
  }
  async function onToggle(card, checked) {
    const nid = card.dataset.nid;
    const input = card.querySelector('input[data-act="toggle"]');
    input.disabled = true;
    card.classList.add("busy");
    try {
      if (checked) await backend.rejoin(nid);
      else await backend.leave(nid);
      toast(checked ? `\u5DF2\u8FDE\u63A5 ${nid}` : `\u5DF2\u65AD\u5F00 ${nid}`);
    } catch (e) {
      input.checked = !checked;
      toast(String(e), "err");
    } finally {
      input.disabled = false;
      card.classList.remove("busy");
      await refresh();
    }
  }
  var actionBusy = false;
  async function onAction(act, nid, btn) {
    if (actionBusy) return;
    actionBusy = true;
    const origText = btn.textContent;
    btn.disabled = true;
    const setPending = (t) => {
      btn.innerHTML = `${SPIN} ${t}`;
    };
    try {
      switch (act) {
        case "info": {
          setPending("\u67E5\u8BE2\u4E2D\u2026");
          const r = await backend.netinfo(nid);
          openModal({ title: `\u7F51\u7EDC\u8BE6\u60C5 \xB7 ${nid}`, body: `<pre class="json">${esc(JSON.stringify(r, null, 2))}</pre>`, wide: true });
          break;
        }
        case "members": {
          setPending("\u67E5\u8BE2\u4E2D\u2026");
          await showMembers(nid);
          break;
        }
        case "settings": {
          await openNetworkSettings(nid);
          break;
        }
        case "subnets": {
          await openSubnetRouteModal(nid);
          break;
        }
        case "invite": {
          setPending("\u751F\u6210\u4E2D\u2026");
          await openInviteModal(nid);
          break;
        }
        case "code": {
          setPending("\u67E5\u8BE2\u4E2D\u2026");
          await openCodeModal(nid);
          break;
        }
        case "delete": {
          if (!await confirmDialog("\u5220\u9664\u7F51\u7EDC", "\u5220\u9664\u5C06\u65AD\u5F00\u6240\u6709\u6210\u5458\u5E76\u91CA\u653E\u7F51\u6BB5\uFF0C\u4E0D\u53EF\u6062\u590D\u3002\u786E\u5B9A\uFF1F", true)) return;
          setPending("\u5220\u9664\u4E2D\u2026");
          const r = await ctlOrClean(nid, "\u5220\u9664\u7F51\u7EDC", () => backend.deleteNet(nid));
          if (r === null) break;
          toast("\u7F51\u7EDC\u5DF2\u5220\u9664");
          break;
        }
        case "remove": {
          if (!await confirmDialog("\u9000\u51FA\u7F51\u7EDC", "\u9000\u51FA\u540E\u9700\u91CD\u65B0\u626B\u7801\u52A0\u5165\u3002\u7EE7\u7EED\uFF1F", true)) return;
          setPending("\u9000\u51FA\u4E2D\u2026");
          await backend.remove(nid);
          toast("\u5DF2\u9000\u51FA\u7F51\u7EDC");
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
  async function ctlOrClean(nid, label, fn) {
    try {
      return await fn();
    } catch (e) {
      if (String(e).includes("\u8BE5\u7F51\u7EDC\u5728\u670D\u52A1\u7AEF\u5DF2\u4E0D\u5B58\u5728")) {
        const ok = await confirmDialog(`${label}\u5931\u8D25`, "\u8BE5\u7F51\u7EDC\u5728\u670D\u52A1\u7AEF\u5DF2\u4E0D\u5B58\u5728\uFF0C\u662F\u5426\u6E05\u7406\u672C\u5730\u914D\u7F6E\uFF1F", true);
        if (ok) {
          await backend.remove(nid);
          toast("\u672C\u5730\u7F51\u7EDC\u914D\u7F6E\u5DF2\u6E05\u7406");
          await refresh();
        }
        return null;
      }
      throw e;
    }
  }
  async function openNetworkSettings(nid) {
    let detail = ownerInfo[nid];
    if (!detail) {
      try {
        detail = await backend.netinfo(nid);
        ownerInfo[nid] = detail;
      } catch (e) {
        toast(`\u83B7\u53D6\u7F51\u7EDC\u4FE1\u606F\u5931\u8D25: ${e}`, "err");
        return;
      }
    }
    const curName = detail.name || "";
    const curSubnet = detail.subnet || "";
    openModal({
      title: `\u7F51\u7EDC\u8BBE\u7F6E \xB7 ${nid}`,
      body: `
      <div class="row"><label>\u7F51\u7EDC\u540D\u79F0</label><input id="s-name" type="text" value="${esc(curName)}" placeholder="\u7559\u7A7A\u4FDD\u6301\u4E0D\u53D8" /></div>
      <div class="row"><label>\u7F51\u6BB5</label>${subnetWidgetHTML({ id: "s-subnet", value: curSubnet, placeholder: "\u7559\u7A7A\u4FDD\u6301\u4E0D\u53D8", emptyHint: "\u7559\u7A7A\u4FDD\u6301\u4E0D\u53D8" })}</div>
      <div class="row"><label class="inline"><input id="s-approval" type="checkbox" ${detail.approvalRequired ? "checked" : ""} /> \u65B0\u6210\u5458\u52A0\u5165\u9700\u521B\u5EFA\u8005\u6279\u51C6</label></div>
      <p class="msg" id="s-warn" hidden>\u4FEE\u6539\u7F51\u6BB5\u4F1A\u91CD\u65B0\u5206\u914D\u6240\u6709\u6210\u5458 IP\uFF0C\u5DF2\u52A0\u5165\u7684 SNET \u5BA2\u6237\u7AEF\u4F1A\u81EA\u52A8\u91CD\u8FDE\uFF1B\u4F46\u624B\u673A\u7B49\u5916\u90E8 WireGuard \u8BBE\u5907\u9700\u624B\u52A8\u91CD\u65B0\u5BFC\u5165\u65B0\u914D\u7F6E\u3002</p>
      <p class="msg" id="s-result"></p>`,
      footer: `<button data-close class="btn ghost">\u53D6\u6D88</button><button id="m-submit" class="btn">\u4FDD\u5B58</button>`,
      onBody: (body) => {
        const subnetWidget = subnetWidgetInit(body.querySelector(".subnet-wrap"));
        const warn = body.querySelector("#s-warn");
        const subnetInput = body.querySelector("#s-subnet");
        subnetInput.addEventListener("input", () => {
          warn.hidden = subnetWidget.get() === "" || subnetWidget.get() === curSubnet;
        });
        body.querySelector("#m-submit").addEventListener("click", async () => {
          const name = body.querySelector("#s-name").value.trim();
          const chk = subnetWidget.check();
          if (!chk.ok) {
            body.querySelector("#s-result").className = "msg";
            body.querySelector("#s-result").textContent = `\u7F51\u6BB5\u65E0\u6548\uFF1A${chk.error}`;
            return;
          }
          const subnet = chk.value;
          const approval = body.querySelector("#s-approval").checked;
          const result = body.querySelector("#s-result");
          const submit = body.querySelector("#m-submit");
          const origText = submit.textContent;
          submit.disabled = true;
          result.className = "msg";
          result.textContent = "\u4FDD\u5B58\u4E2D\u2026";
          try {
            const r = await ctlOrClean(nid, "\u4FDD\u5B58\u8BBE\u7F6E", async () => {
              await backend.updateSettings({
                nid,
                name,
                subnet: subnet !== curSubnet ? subnet : "",
                approvalRequired: approval !== !!detail.approvalRequired ? approval : null
              });
              return true;
            });
            if (r === null) return;
            result.className = "msg ok";
            result.textContent = "\u5DF2\u4FDD\u5B58";
            await refreshOwnerInfo(true);
            toast("\u7F51\u7EDC\u8BBE\u7F6E\u5DF2\u4FDD\u5B58");
            closeModal();
          } catch (e) {
            result.className = "msg";
            result.textContent = `\u4FDD\u5B58\u5931\u8D25: ${e}`;
            submit.disabled = false;
            submit.textContent = origText;
          }
        });
      }
    });
  }
  async function openSubnetRouteModal(nid) {
    const st = await backend.status();
    const net = (st?.networks ?? []).find((n) => n.networkId === nid);
    const curSubnets = net?.allowedSubnets ?? [];
    const renderTags = (tags) => tags.length ? tags.map((s) => `<span class="chip">${esc(s)}<button data-del="${esc(s)}" class="chip-del" title="\u79FB\u9664">&times;</button></span>`).join("") : `<span class="muted">\u672A\u5BA3\u544A\u4EFB\u4F55\u5B50\u7F51</span>`;
    openModal({
      title: `\u5B50\u7F51\u8DEF\u7531 \xB7 ${nid}`,
      body: `
      <p class="hint">\u5BA3\u544A\u672C\u8BBE\u5907\u7684\u5C40\u57DF\u7F51\u5B50\u7F51\uFF0C\u5176\u4ED6\u6210\u5458\u53EF\u901A\u8FC7 VPN \u8BBF\u95EE\u3002</p>
      <div id="sr-suggest" class="subnet-tags" style="margin-bottom:2px"></div>
      <div id="sr-tags" class="subnet-tags">${renderTags(curSubnets)}</div>
      <div class="subnet-add-row">
        <input type="text" id="sr-input" placeholder="\u5982 192.168.3.0/24" />
        <button id="sr-add" class="btn ghost sm">\u6DFB\u52A0</button>
      </div>
      <p class="msg" style="margin-top:8px">IP \u8F6C\u53D1\u5C06\u7531\u540E\u53F0\u670D\u52A1\u81EA\u52A8\u5F00\u542F\uFF0C\u65E0\u9700\u624B\u52A8\u914D\u7F6E\u3002</p>
      <p class="msg" id="sr-result"></p>`,
      footer: `<button data-close class="btn ghost">\u53D6\u6D88</button><button id="sr-save" class="btn">\u4FDD\u5B58</button>`,
      onBody: (body) => {
        const tags = [...curSubnets];
        const tagsEl = body.querySelector("#sr-tags");
        const suggestEl = body.querySelector("#sr-suggest");
        const input = body.querySelector("#sr-input");
        const result = body.querySelector("#sr-result");
        const rerender = () => {
          tagsEl.innerHTML = renderTags(tags);
        };
        const refreshSuggest = (localSubnets2) => {
          const suggestions = localSubnets2.filter((s) => !tags.includes(s));
          if (!suggestions.length) {
            suggestEl.innerHTML = "";
            return;
          }
          suggestEl.innerHTML = `<span class="muted" style="width:100%;margin-bottom:2px">\u68C0\u6D4B\u5230\u7684\u672C\u5730\u5B50\u7F51</span>` + suggestions.map((s) => `<span class="chip" data-add="${esc(s)}" style="cursor:pointer">${esc(s)} <span style="opacity:0.5">+</span></span>`).join("");
        };
        let localSubnets = [];
        backend.detectLocalSubnets().then((arr) => {
          localSubnets = arr;
          refreshSuggest(arr);
        }).catch(() => {
        });
        suggestEl.addEventListener("click", (e) => {
          const chip = e.target.closest("[data-add]");
          if (!chip) return;
          const cidr = chip.dataset.add;
          if (tags.includes(cidr)) return;
          tags.push(cidr);
          rerender();
          refreshSuggest(localSubnets);
        });
        body.querySelector("#sr-add").addEventListener("click", () => {
          const raw = input.value.trim();
          if (!raw) return;
          if (tags.includes(raw)) {
            result.textContent = "\u8BE5\u5B50\u7F51\u5DF2\u6DFB\u52A0";
            return;
          }
          const chk = subnetCheck(raw, 24);
          if (!chk.ok) {
            result.className = "msg";
            result.textContent = `\u65E0\u6548: ${chk.error}`;
            return;
          }
          const cidr = chk.value;
          if (tags.includes(cidr)) {
            result.textContent = "\u8BE5\u5B50\u7F51\u5DF2\u6DFB\u52A0";
            return;
          }
          tags.push(cidr);
          input.value = "";
          result.textContent = "";
          rerender();
          refreshSuggest(localSubnets);
        });
        input.addEventListener("keydown", (e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            body.querySelector("#sr-add").click();
          }
        });
        tagsEl.addEventListener("click", (e) => {
          const btn = e.target.closest("[data-del]");
          if (!btn) return;
          const val = btn.dataset.del;
          const idx = tags.indexOf(val);
          if (idx >= 0) tags.splice(idx, 1);
          rerender();
          refreshSuggest(localSubnets);
        });
        body.querySelector("#sr-save").addEventListener("click", async () => {
          const saveBtn = body.querySelector("#sr-save");
          const origText = saveBtn.textContent;
          saveBtn.disabled = true;
          result.className = "msg";
          result.textContent = "\u4FDD\u5B58\u4E2D\u2026";
          try {
            const r = await ctlOrClean(nid, "\u5B50\u7F51\u8DEF\u7531", async () => {
              await backend.updateSubnets({ nid, subnets: tags });
              return true;
            });
            if (r === null) return;
            result.className = "msg ok";
            result.textContent = "\u5DF2\u4FDD\u5B58";
            toast("\u5B50\u7F51\u8DEF\u7531\u5DF2\u66F4\u65B0");
            closeModal();
          } catch (e) {
            result.className = "msg";
            result.textContent = `\u4FDD\u5B58\u5931\u8D25: ${e}`;
            saveBtn.disabled = false;
            saveBtn.textContent = origText;
          }
        });
      }
    });
  }
  async function openCodeModal(nid) {
    let code = "";
    try {
      const detail = await backend.netinfo(nid);
      code = detail.pairingCode || "";
    } catch (e) {
      toast(`\u83B7\u53D6\u914D\u5BF9\u7801\u5931\u8D25: ${e}`, "err");
      return;
    }
    if (!code) {
      const nr = await ctlOrClean(nid, "\u67E5\u770B\u914D\u5BF9\u7801", () => backend.resetCode(nid));
      if (nr === null) return;
      code = nr.pairingCode;
      toast("\u539F\u914D\u5BF9\u7801\u4E0D\u53EF\u7528\uFF0C\u5DF2\u751F\u6210\u65B0\u7801\uFF08\u65E7\u7801\u4F5C\u5E9F\uFF09", "warn");
    }
    openModal({
      title: `\u67E5\u770B\u914D\u5BF9\u7801 \xB7 ${nid}`,
      body: `
      <div class="kv"><span>\u914D\u5BF9\u7801</span><code id="pc-code" style="letter-spacing:1.5px">${esc(code)}</code></div>
      <p class="hint" id="pc-note">\u914D\u5BF9\u7801\u542B\u6709\u6548\u6B21\u6570\u4E0E\u65F6\u9650\uFF1B\u590D\u5236\u540E\u5206\u4EAB\u7ED9\u5176\u4ED6\u8BBE\u5907\u5373\u53EF\u52A0\u5165\u3002</p>
      <p class="msg" id="pc-result"></p>`,
      footer: `<button data-close class="btn ghost">\u5173\u95ED</button><button id="pc-copy" class="btn">\u590D\u5236\u914D\u5BF9\u7801</button><button id="pc-reset" class="btn danger">\u91CD\u7F6E\u914D\u5BF9\u7801</button>`,
      onBody: (m) => {
        const codeEl = m.querySelector("#pc-code");
        const result = m.querySelector("#pc-result");
        const note = m.querySelector("#pc-note");
        const copy = m.querySelector("#pc-copy");
        const reset = m.querySelector("#pc-reset");
        copy.addEventListener("click", () => void copyText(code, copy));
        reset.addEventListener("click", async () => {
          if (!await confirmDialog("\u91CD\u7F6E\u914D\u5BF9\u7801", "\u4F5C\u5E9F\u65E7\u914D\u5BF9\u7801\u5E76\u751F\u6210\u65B0\u7801\uFF1F\u65E7\u7801\u7ACB\u5373\u5931\u6548\uFF0C\u5DF2\u52A0\u5165\u6210\u5458\u4E0D\u53D7\u5F71\u54CD\u3002", true)) return;
          reset.disabled = true;
          reset.innerHTML = `${SPIN} \u751F\u6210\u4E2D\u2026`;
          try {
            const r = await ctlOrClean(nid, "\u91CD\u7F6E\u914D\u5BF9\u7801", () => backend.resetCode(nid));
            if (r === null) {
              reset.disabled = false;
              reset.textContent = "\u91CD\u7F6E\u914D\u5BF9\u7801";
              return;
            }
            code = r.pairingCode;
            codeEl.textContent = code;
            result.className = "msg ok";
            result.textContent = "\u65E7\u7801\u5DF2\u5931\u6548\uFF0C\u5DF2\u52A0\u5165\u6210\u5458\u4E0D\u53D7\u5F71\u54CD\u3002";
            note.textContent = "";
            toast("\u5DF2\u751F\u6210\u65B0\u914D\u5BF9\u7801");
          } catch (e) {
            result.className = "msg";
            result.textContent = `\u91CD\u7F6E\u5931\u8D25: ${e}`;
          } finally {
            reset.disabled = false;
            reset.textContent = "\u91CD\u7F6E\u914D\u5BF9\u7801";
          }
          await refresh();
        });
      }
    });
  }
  async function openInviteModal(nid) {
    let detail;
    try {
      detail = await backend.netinfo(nid);
    } catch (e) {
      toast(`\u83B7\u53D6\u9080\u8BF7\u4FE1\u606F\u5931\u8D25: ${e}`, "err");
      return;
    }
    let code = detail.pairingCode;
    if (!code) {
      const nr = await ctlOrClean(nid, "\u9080\u8BF7\u52A0\u5165", () => backend.resetCode(nid));
      if (nr === null) return;
      code = nr.pairingCode;
      toast("\u539F\u914D\u5BF9\u7801\u4E0D\u53EF\u7528\uFF0C\u5DF2\u751F\u6210\u65B0\u7801\uFF08\u65E7\u7801\u4F5C\u5E9F\uFF09", "warn");
    }
    const netId = detail.id || detail.networkId;
    if (!netId) {
      toast("\u83B7\u53D6\u7F51\u7EDCID\u5931\u8D25", "err");
      return;
    }
    const link = `snet://join?nid=${encodeURIComponent(netId)}&code=${encodeURIComponent(code)}`;
    const name = detail.name || netId;
    openModal({
      title: `\u9080\u8BF7\u52A0\u5165 \xB7 ${name}`,
      wide: true,
      body: `
      <p class="hint">\u88AB\u9080\u8BF7\u8BBE\u5907\u626B\u63CF\u4E0B\u65B9\u4E8C\u7EF4\u7801\uFF0C\u6216\u5728 App \u4E2D\u9009\u62E9\u300C\u52A0\u5165\u7F51\u7EDC\u300D\u7C98\u8D34\u9080\u8BF7\u94FE\u63A5\u5373\u53EF\u52A0\u5165\u3002</p>
      <div class="qr" id="qr"></div>
      <div class="kv"><span>\u9080\u8BF7\u94FE\u63A5</span><code>${esc(link)}</code></div>
      <div class="kv"><span>\u7F51\u7EDCID</span><code>${esc(netId)}</code></div>
      <div class="kv"><span>\u914D\u5BF9\u7801</span><code>${esc(code)}</code></div>
      <p class="hint">\u914D\u5BF9\u7801\u542B\u6709\u6548\u6B21\u6570\u4E0E\u65F6\u9650\uFF0C\u53EF\u5728\u5361\u7247\u4E0A\u300C\u67E5\u770B\u914D\u5BF9\u7801\u300D\u968F\u65F6\u67E5\u770B\u6216\u4F5C\u5E9F\u91CD\u53D1\u3002</p>`,
      footer: `<button data-close class="btn ghost">\u5173\u95ED</button><button id="m-copy" class="btn">\u590D\u5236\u9080\u8BF7\u94FE\u63A5</button>`,
      onBody: (body) => {
        renderQR(body.querySelector("#qr"), link);
        body.querySelector("#m-copy")?.addEventListener("click", async (e) => {
          await copyText(link, e.currentTarget);
        });
      }
    });
  }
  async function showMembers(nid) {
    const mine = (status?.networks ?? []).find((n) => n.networkId === nid);
    const myIp = mine?.ip;
    if (mine?.owner) {
      const r2 = await backend.netinfo(nid);
      ownerInfo[nid] = r2;
      const rows2 = (r2.nodes ?? []).map((nd) => {
        const online = nd.online ? '<span class="pill ok">\u5728\u7EBF</span>' : '<span class="pill off">\u79BB\u7EBF</span>';
        const kick = nd.ip === myIp ? `<span class="muted">\u81EA\u5DF1</span>` : `<button data-node="${esc(nd.id)}" class="btn danger ghost sm">\u8E22\u51FA</button>`;
        const subnets = (nd.allowedSubnets?.length ?? 0) > 0 ? `<span class="pill subnet-route">${esc(nd.allowedSubnets.join(", "))}</span>` : `<span class="muted">-</span>`;
        return `<tr><td class="mono">${esc(nd.ip)}</td><td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td><td>${online}</td><td>${subnets}</td><td>${kick}</td></tr>`;
      }).join("");
      const pendingRows = (r2.pending ?? []).map((p) => {
        return `<tr><td colspan="2"><span class="muted">\u8BBE\u5907</span> <code>${p.deviceId ? esc(p.deviceId.slice(0, 8)) + "\u2026" : "-"}</code><span class="muted"> \u516C\u94A5</span> <code>${esc(p.publicKey.slice(0, 12))}\u2026</code></td><td><span class="pill warn">\u5F85\u6279\u51C6</span></td><td>-</td><td><button data-pend="${esc(p.id)}" class="btn sm" style="background:var(--ok)">\u6279\u51C6</button> <button data-pend="${esc(p.id)}" class="btn danger sm">\u62D2\u7EDD</button></td></tr>`;
      }).join("");
      const pendingSection = pendingRows ? `<div class="pending-block"><h4>\u5F85\u6279\u51C6\u52A0\u5165\u8BF7\u6C42</h4><div class="tbl-wrap"><table class="members"><tbody>${pendingRows}</tbody></table></div></div>` : "";
      openModal({
        title: `\u6210\u5458 \xB7 ${nid}`,
        wide: true,
        body: `<div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>\u8BBE\u5907</th><th>\u72B6\u6001</th><th>\u5B50\u7F51\u8DEF\u7531</th><th></th></tr></thead><tbody>${rows2}</tbody></table></div>${pendingSection}`,
        onBody: (b) => {
          b.querySelectorAll("[data-node]").forEach(
            (k) => k.addEventListener("click", async () => {
              if (!await confirmDialog("\u8E22\u51FA\u6210\u5458", "\u8E22\u51FA\u8BE5\u6210\u5458\uFF1F\u8BE5\u8BBE\u5907\u5C06\u7ACB\u5373\u65AD\u5F00\u3002", true)) return;
              const btn = k;
              const orig = btn.textContent;
              btn.disabled = true;
              btn.textContent = "\u8E22\u51FA\u4E2D\u2026";
              try {
                const r3 = await ctlOrClean(nid, "\u8E22\u51FA\u6210\u5458", () => backend.kick({ nid, nodeId: k.dataset.node }));
                if (r3 === null) return;
                toast("\u5DF2\u8E22\u51FA");
                closeModal();
              } catch (e) {
                toast(String(e), "err");
                btn.disabled = false;
                btn.textContent = orig;
              }
              await refresh();
            })
          );
          b.querySelectorAll("[data-pend]").forEach((k) => {
            const deny = k.classList.contains("danger");
            k.addEventListener("click", async () => {
              const pid = k.dataset.pend;
              if (!await confirmDialog(deny ? "\u62D2\u7EDD\u52A0\u5165\u8BF7\u6C42" : "\u6279\u51C6\u52A0\u5165\u8BF7\u6C42", deny ? "\u62D2\u7EDD\u540E\u8BE5\u8BBE\u5907\u65E0\u6CD5\u52A0\u5165\u3002\u7EE7\u7EED\uFF1F" : "\u6279\u51C6\u540E\u8BE5\u8BBE\u5907\u7ACB\u5373\u52A0\u5165\u7F51\u7EDC\u3002\u7EE7\u7EED\uFF1F", deny)) return;
              const btn = k;
              const orig = btn.textContent;
              btn.disabled = true;
              btn.textContent = deny ? "\u62D2\u7EDD\u4E2D\u2026" : "\u6279\u51C6\u4E2D\u2026";
              try {
                if (deny) await backend.deny({ nid, pendingId: pid });
                else await backend.approve({ nid, pendingId: pid });
                toast(deny ? "\u5DF2\u62D2\u7EDD" : "\u5DF2\u6279\u51C6");
                closeModal();
              } catch (e) {
                toast(String(e), "err");
                btn.disabled = false;
                btn.textContent = orig;
              }
              await refresh();
            });
          });
        }
      });
      return;
    }
    const r = await backend.peers(nid);
    const all = r.self ? [r.self, ...r.peers ?? []] : r.peers ?? [];
    const rows = all.map((nd) => {
      const isSelf = nd.ip === myIp;
      const online = nd.online ? '<span class="pill ok">\u5728\u7EBF</span>' : '<span class="pill off">\u79BB\u7EBF</span>';
      const subnets = (nd.allowedSubnets?.length ?? 0) > 0 ? `<span class="pill subnet-route">${esc(nd.allowedSubnets.join(", "))}</span>` : `<span class="muted">-</span>`;
      return `<tr><td class="mono">${esc(nd.ip)}</td><td>${nd.deviceId ? `<span class="muted mono">${esc(nd.deviceId.slice(0, 8))}</span>` : "-"}</td><td>${online}</td><td>${subnets}</td><td>${isSelf ? `<span class="muted">\u81EA\u5DF1</span>` : ""}</td></tr>`;
    }).join("");
    openModal({
      title: `\u6210\u5458 \xB7 ${nid}`,
      wide: true,
      body: `<p class="hint">\u6210\u5458\u5217\u8868\uFF08\u53EA\u8BFB\uFF0C\u672C\u673A\u975E\u521B\u5EFA\u8005\uFF09</p><div class="tbl-wrap"><table class="members"><thead><tr><th>IP</th><th>\u8BBE\u5907</th><th>\u72B6\u6001</th><th>\u5B50\u7F51\u8DEF\u7531</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`
    });
  }
  function openCreateModal() {
    const s = loadSettings();
    const hasOwner = (status?.networks ?? []).some((n) => n.owner);
    const srv = currentServer();
    const hint = hasOwner ? `<p class="msg">\u672C\u8BBE\u5907\u5DF2\u521B\u5EFA\u7F51\u7EDC\uFF08\u6BCF\u5BA2\u6237\u7AEF\u4EC5\u80FD\u521B\u5EFA\u4E00\u4E2A\uFF09\u3002\u5982\u9700\u65B0\u7F51\u7EDC\uFF0C\u8BF7\u5148\u5220\u9664\u6216\u9000\u51FA\u5F53\u524D\u7F51\u7EDC\u3002</p>` : "";
    const serverHint = srv ? `<p class="hint">\u5C06\u5728\u5DF2\u8FDE\u63A5\u7684\u670D\u52A1\u5668\u4E0A\u521B\u5EFA\uFF1A<code>${esc(srv)}</code></p>` : `<p class="msg">\u672A\u8FDE\u63A5\u670D\u52A1\u5668\uFF1A\u8BF7\u5148\u5728\u300C\u8BBE\u7F6E\u300D\u4E2D\u94FE\u63A5\u670D\u52A1\u5668\u3002</p>`;
    openModal({
      title: "\u521B\u5EFA\u7F51\u7EDC",
      body: `${hint}
      <div class="row"><label>\u7F51\u7EDC\u540D\u79F0</label><input id="m-name" type="text" placeholder="\u4F8B\u5982\uFF1A\u5BB6\u5EAD\u7F51\u7EDC" /></div>
      <div class="row"><label>\u7F51\u6BB5</label>${subnetWidgetHTML({ id: "m-subnet", placeholder: "\u7559\u7A7A\u81EA\u52A8\u5206\u914D\uFF08\u5982 10.88.0.0/24\uFF09", emptyHint: "\u7559\u7A7A\u81EA\u52A8\u5206\u914D\uFF0C\u901A\u5E38\u4E3A 10.88.N.0/24" })}</div>
      ${serverHint}
      <div class="row"><label>WireGuard \u7AEF\u53E3</label><input id="m-port" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="row"><label>CA \u8BC1\u4E66\u8DEF\u5F84</label><input id="m-ca" type="text" value="${esc(s.ca)}" placeholder="\u516C\u5171\u8BC1\u4E66(\u5982 Let's Encrypt)\u7559\u7A7A\uFF1B\u81EA\u7B7E\u540D\u670D\u52A1\u5668\u586B\u8BC1\u4E66\u8DEF\u5F84" /></div>
      <p class="msg" id="m-result"></p>`,
      footer: `<button data-close class="btn ghost">\u53D6\u6D88</button><button id="m-submit" class="btn" ${hasOwner || !srv ? "disabled" : ""}>\u521B\u5EFA</button>`,
      onBody: (body) => {
        const submit = body.querySelector("#m-submit");
        const result = body.querySelector("#m-result");
        const subnetWidget = subnetWidgetInit(body.querySelector(".subnet-wrap"));
        submit.addEventListener("click", async () => {
          submit.disabled = true;
          submit.innerHTML = `${SPIN} \u521B\u5EFA\u4E2D\u2026`;
          result.className = "msg";
          result.textContent = "";
          try {
            const name = body.querySelector("#m-name").value.trim();
            const chk = subnetWidget.check();
            if (!chk.ok) throw new Error(`\u7F51\u6BB5\u65E0\u6548\uFF1A${chk.error}`);
            const subnet = chk.value;
            const server = currentServer();
            const port = Number(body.querySelector("#m-port").value);
            const ca = body.querySelector("#m-ca").value.trim();
            if (!name) throw new Error("\u8BF7\u8F93\u5165\u7F51\u7EDC\u540D\u79F0");
            if (!server) throw new Error("\u672A\u8FDE\u63A5\u670D\u52A1\u5668\uFF1A\u8BF7\u5148\u5728\u8BBE\u7F6E\u4E2D\u94FE\u63A5\u670D\u52A1\u5668");
            const r = await backend.create({ server, port, ca, name, subnet, approvalRequired: false });
            const modalEl = body;
            const mBody = modalEl.querySelector(".modal-body");
            const mFoot = modalEl.querySelector(".modal-foot");
            mBody.innerHTML = `
            <div class="create-ok">
              <p class="msg ok">\u7F51\u7EDC\u521B\u5EFA\u6210\u529F\uFF0C\u9080\u8BF7\u5176\u4ED6\u8BBE\u5907\u52A0\u5165\uFF1A</p>
              <div class="qr" id="qr"></div>
              <div class="kv"><span>\u7F51\u7EDCID</span><code>${esc(r.networkId)}</code></div>
              <div class="kv"><span>\u914D\u5BF9\u7801</span><code>${esc(r.pairingCode)}</code></div>
              <div class="kv"><span>\u9080\u8BF7\u94FE\u63A5</span><code>${esc(r.link)}</code></div>
            </div>`;
            renderQR(mBody.querySelector("#qr"), r.link);
            mFoot.innerHTML = `<button id="m-copy" class="btn">\u590D\u5236\u9080\u8BF7\u94FE\u63A5</button><button id="m-close" class="btn ghost">\u5173\u95ED</button>`;
            mFoot.querySelector("#m-copy").addEventListener("click", async (e) => {
              await copyText(r.link, e.currentTarget);
            });
            mFoot.querySelector("#m-close").addEventListener("click", closeModal);
            await refresh();
          } catch (e) {
            result.className = "msg";
            result.textContent = `\u521B\u5EFA\u5931\u8D25: ${e}`;
            submit.disabled = false;
            submit.innerHTML = "\u521B\u5EFA";
          }
        });
      }
    });
  }
  function normalizeServerCompare(s) {
    return s.replace(/\/+$/, "");
  }
  function linkServerOf(link) {
    try {
      const u = new URL(link);
      if (u.protocol === "snet:" || u.protocol === "http:" || u.protocol === "https:") {
        return u.searchParams.get("server") || void 0;
      }
    } catch {
    }
    return void 0;
  }
  function openJoinModal() {
    const s = loadSettings();
    const srv = currentServer();
    const serverHint = srv ? `<p class="hint">\u5C06\u52A0\u5165\u5DF2\u8FDE\u63A5\u670D\u52A1\u5668\u4E0A\u7684\u7F51\u7EDC\uFF1A<code>${esc(srv)}</code></p>` : `<p class="msg">\u672A\u8FDE\u63A5\u670D\u52A1\u5668\uFF1A\u8BF7\u5148\u5728\u300C\u8BBE\u7F6E\u300D\u4E2D\u94FE\u63A5\u670D\u52A1\u5668\uFF1B\u6216\u7C98\u8D34\u5E26\u6709\u670D\u52A1\u5668\u5730\u5740\u7684\u9080\u8BF7\u94FE\u63A5\u540E\u52A0\u5165\u3002</p>`;
    openModal({
      title: "\u52A0\u5165\u7F51\u7EDC",
      body: `<div class="row"><label>\u9080\u8BF7\u94FE\u63A5</label><input id="m-link" type="text" placeholder="snet://join?nid=...&code=..." /></div>
      <p class="hint" style="text-align:center">\u6216\u624B\u52A8\u8F93\u5165</p>
      <div class="row"><label>\u7F51\u7EDCID</label><input id="m-nid" type="text" placeholder="6 \u4F4D\u7F51\u7EDCID" /></div>
      <div class="row"><label>\u914D\u5BF9\u7801</label><input id="m-code" type="text" placeholder="12 \u4F4D\u914D\u5BF9\u7801" /></div>
      ${serverHint}
      <div class="row"><label>WireGuard \u7AEF\u53E3</label><input id="m-port" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="row"><label>CA \u8BC1\u4E66\u8DEF\u5F84</label><input id="m-ca" type="text" value="${esc(s.ca)}" placeholder="\u516C\u5171\u8BC1\u4E66(\u5982 Let's Encrypt)\u7559\u7A7A\uFF1B\u81EA\u7B7E\u540D\u670D\u52A1\u5668\u586B\u8BC1\u4E66\u8DEF\u5F84" /></div>
      <div id="m-bind-auth" hidden></div>
      <p class="msg" id="m-result"></p>`,
      footer: `<button data-close class="btn ghost">\u53D6\u6D88</button><button id="m-submit" class="btn">\u52A0\u5165</button>`,
      onBody: (body) => {
        const submit = body.querySelector("#m-submit");
        const result = body.querySelector("#m-result");
        const authWrap = body.querySelector("#m-bind-auth");
        const caInput = body.querySelector("#m-ca");
        const bindAndThen = (server, then) => {
          authWrap.hidden = false;
          authWrap.innerHTML = `
          <div class="settings-block">
            <p class="msg">\u8BE5\u9080\u8BF7\u5C5E\u4E8E\u670D\u52A1\u5668 <code>${esc(server)}</code>\uFF0C\u672C\u673A\u5C1A\u672A\u7ED1\u5B9A\u8BE5\u670D\u52A1\u5668\u3002\u8BF7\u8F93\u5165\u8BBE\u5907\u6388\u6743\u7801\u4EE5\u7ED1\u5B9A\u540E\u52A0\u5165\uFF1A</p>
            <div class="row"><label>\u8BBE\u5907\u6388\u6743\u7801</label><input id="m-bind-code" type="text" placeholder="\u7BA1\u7406\u7AEF\u751F\u6210\u7684\u6388\u6743\u7801" autocomplete="off" /></div>
            <div class="settings-actions"><button id="m-bind-go" class="btn">\u7ED1\u5B9A\u5E76\u52A0\u5165</button></div>
            <p class="msg" id="m-bind-result"></p>
          </div>`;
          const codeInput = authWrap.querySelector("#m-bind-code");
          const bindResult = authWrap.querySelector("#m-bind-result");
          const bindBtn = authWrap.querySelector("#m-bind-go");
          codeInput.focus();
          const go = async () => {
            const code = codeInput.value.trim();
            if (!code) {
              bindResult.className = "msg";
              bindResult.textContent = "\u8BF7\u8F93\u5165\u8BBE\u5907\u6388\u6743\u7801";
              return;
            }
            bindBtn.disabled = true;
            bindResult.className = "msg";
            bindResult.textContent = "\u6B63\u5728\u7ED1\u5B9A\u2026";
            try {
              const ca = caInput.value.trim();
              await backend.bind({ server, ca, code });
              saveSettings({ ...loadSettings(), server, ca });
              bindResult.className = "msg ok";
              bindResult.textContent = `\u5DF2\u7ED1\u5B9A ${server}\uFF0C\u6B63\u5728\u52A0\u5165\u2026`;
              await then();
            } catch (e) {
              bindResult.className = "msg";
              bindResult.textContent = `\u7ED1\u5B9A\u5931\u8D25: ${e}`;
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
          submit.innerHTML = `${SPIN} \u52A0\u5165\u4E2D\u2026`;
          result.className = "msg";
          result.textContent = "";
          try {
            const port = Number(body.querySelector("#m-port").value);
            const ca = caInput.value.trim();
            let link = body.querySelector("#m-link").value.trim();
            if (!link) {
              const nid = body.querySelector("#m-nid").value.trim();
              const code = body.querySelector("#m-code").value.trim();
              if (!nid || !code) throw new Error("\u8BF7\u8F93\u5165\u9080\u8BF7\u94FE\u63A5\uFF0C\u6216\u7F51\u7EDCID + \u914D\u5BF9\u7801");
              link = `snet://join?nid=${nid}&code=${code}`;
            }
            const linkServer = linkServerOf(link);
            const srv2 = currentServer();
            class NeedBind extends Error {
            }
            const doJoin = async () => {
              const server = linkServer && linkServer.trim() ? linkServer : srv2;
              if (!server) throw new Error("\u672A\u8FDE\u63A5\u670D\u52A1\u5668\uFF1A\u8BF7\u5148\u5728\u8BBE\u7F6E\u4E2D\u94FE\u63A5\u670D\u52A1\u5668");
              try {
                const r = await backend.join({ server, port, ca, link });
                result.className = "msg ok";
                if (r.status === "pending") {
                  result.innerHTML = `<p>\u5DF2\u63D0\u4EA4\u52A0\u5165\u8BF7\u6C42\uFF0C\u7B49\u5F85\u7F51\u7EDC\u521B\u5EFA\u8005\u6279\u51C6\u3002</p><p class="hint">\u6279\u51C6\u540E\u672C\u673A\u4F1A\u81EA\u52A8\u52A0\u5165\u5E76\u8FDE\u63A5\uFF1B\u4E5F\u53EF\u5728\u4E0A\u65B9\u300C\u5F85\u6279\u51C6 \xB7 \u52A0\u5165\u8BF7\u6C42\u300D\u5361\u7247\u4E2D\u53D6\u6D88\u3002</p>`;
                } else {
                  result.textContent = `\u5DF2\u52A0\u5165: IP ${r.ip ?? "-"}\uFF0C\u7F51\u7EDC ${r.networkId ?? "-"}`;
                }
                skipOnboarding = true;
                await refresh();
                skipOnboarding = false;
                closeModal();
              } catch (e) {
                if (String(e).includes("\u8BBE\u5907\u672A\u6388\u6743")) throw new NeedBind();
                throw e;
              }
            };
            if (linkServer && srv2 && normalizeServerCompare(linkServer) === normalizeServerCompare(srv2)) {
              await doJoin();
            } else if (linkServer) {
              try {
                await doJoin();
              } catch (e) {
                if (e instanceof NeedBind) {
                  await new Promise((resolve, reject) => {
                    bindAndThen(linkServer, async () => {
                      try {
                        await doJoin();
                        resolve();
                      } catch (e2) {
                        result.className = "msg";
                        result.textContent = `\u52A0\u5165\u5931\u8D25: ${e2}`;
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
            result.textContent = `\u52A0\u5165\u5931\u8D25: ${e}`;
            submit.disabled = false;
            submit.innerHTML = "\u52A0\u5165";
          }
        });
      }
    });
  }
  var HELP_ROWS = [
    ["\u94FE\u63A5\u5F00\u5173", "\u5168\u90E8", "\u5F00\u542F\uFF1D\u8FDE\u63A5\u8BE5\u7F51\u7EDC\u96A7\u9053\uFF1B\u5173\u95ED\uFF1D\u65AD\u5F00\u672C\u673A\u8BE5\u7F51\u7EDC\uFF0C\u4FDD\u7559\u914D\u7F6E\u4E0E\u670D\u52A1\u5668\u8282\u70B9\uFF0C\u53EF\u968F\u65F6\u518D\u5F00"],
    ["\u8BE6\u60C5", "\u521B\u5EFA\u8005", "\u67E5\u770B\u670D\u52A1\u5668\u4E0A\u8BE5\u7F51\u7EDC\u7684\u5B8C\u6574\u4FE1\u606F\uFF08\u6210\u5458\u3001\u4E2D\u7EE7\u7AEF\u53E3\u3001\u5728\u7EBF\u72B6\u6001\u3001\u521B\u5EFA\u65F6\u95F4\u7B49\uFF09"],
    ["\u6210\u5458", "\u521B\u5EFA\u8005", "\u67E5\u770B\u6210\u5458\u5217\u8868\u4E0E\u5728\u7EBF\u72B6\u6001\uFF1B\u6279\u51C6/\u62D2\u7EDD\u5F85\u6279\u51C6\u52A0\u5165\u8BF7\u6C42\u3001\u8E22\u51FA\u6210\u5458"],
    ["\u8BBE\u7F6E", "\u521B\u5EFA\u8005", "\u4FEE\u6539\u7F51\u7EDC\u540D\u79F0\u3001\u7F51\u6BB5\uFF0C\u6216\u5F00\u542F\u300C\u65B0\u6210\u5458\u9700\u6279\u51C6\u300D"],
    ["\u67E5\u770B\u914D\u5BF9\u7801", "\u521B\u5EFA\u8005", "\u67E5\u770B\u5F53\u524D\u914D\u5BF9\u7801\u5E76\u590D\u5236\uFF1B\u53EF\u4F5C\u5E9F\u65E7\u7801\u5E76\u751F\u6210\u65B0\u7801\uFF0C\u65E7\u7801\u7ACB\u5373\u5931\u6548\u3001\u5DF2\u52A0\u5165\u6210\u5458\u4E0D\u53D7\u5F71\u54CD"],
    ["\u5220\u9664", "\u521B\u5EFA\u8005", "\u5F7B\u5E95\u5220\u9664\u8BE5\u7F51\u7EDC\uFF1A\u6240\u6709\u6210\u5458\u65AD\u5F00\u3001\u7F51\u6BB5\u91CA\u653E\uFF0C\u4E0D\u53EF\u6062\u590D"],
    ["\u9000\u51FA\u7F51\u7EDC", "\u6210\u5458", "\u672C\u673A\u79FB\u51FA\u8BE5\u7F51\u7EDC\u5E76\u9057\u5FD8\u914D\u7F6E\uFF0C\u9700\u91CD\u65B0\u626B\u7801\u52A0\u5165\uFF1B\u521B\u5EFA\u8005\u65E0\u6B64\u6309\u94AE"]
  ];
  function openSettingsModal() {
    const s = loadSettings();
    const helpRows = HELP_ROWS.map(([op, who, desc]) => `<tr><td>${op}</td><td>${who}</td><td>${desc}</td></tr>`).join("");
    openModal({
      title: "\u8BBE\u7F6E",
      body: `<div class="settings-block">
        <div class="row"><label>\u670D\u52A1\u5668\u5730\u5740</label><input id="s-server" type="text" value="${esc(s.server)}" placeholder="https://example.com:8090" /></div>
        <div class="row"><label>\u8BBE\u5907\u6388\u6743\u7801</label><input id="s-code" type="text" placeholder="\u7BA1\u7406\u7AEF\u751F\u6210\u7684\u8BBE\u5907\u6388\u6743\u7801\uFF08\u4EC5\u7528\u4E8E\u94FE\u63A5\uFF0C\u4E0D\u4FDD\u5B58\uFF09" autocomplete="off" /></div>
        <div class="row"><label>CA \u8BC1\u4E66\u8DEF\u5F84</label><input id="s-ca" type="text" value="${esc(s.ca)}" placeholder="\u516C\u5171\u8BC1\u4E66(\u5982 Let's Encrypt)\u7559\u7A7A\uFF1B\u81EA\u7B7E\u540D\u670D\u52A1\u5668\u586B\u8BC1\u4E66\u8DEF\u5F84" /></div>
        <div class="settings-actions">
          <button id="s-bind" class="btn">\u94FE\u63A5\u670D\u52A1\u5668</button>
        </div>
        <p class="msg" id="s-bind-result"></p>
      </div>
      <div class="row"><label>WireGuard \u7AEF\u53E3</label><input id="s-wgport" type="number" min="1024" max="65535" value="${s.wgport}" /></div>
      <div class="settings-actions" id="s-daemon-row">
        ${backend.hasDaemonControl ? status ? `<span class="live-dot ok">\u8FD0\u884C\u4E2D</span>` : `<button id="s-start-daemon" class="btn ghost">\u542F\u52A8</button>` : ""}
      </div>
      <p class="msg" id="s-msg"></p>
      <details class="help">
        <summary>\u64CD\u4F5C\u8BF4\u660E</summary>
        <div class="tbl-wrap">
        <table class="help">
          <thead><tr><th>\u64CD\u4F5C</th><th>\u9002\u7528</th><th>\u4F5C\u7528</th></tr></thead>
          <tbody>${helpRows}</tbody>
        </table>
        </div>
      </details>`,
      wide: true,
      footer: `<button data-close class="btn ghost">\u5173\u95ED</button>`,
      onBody: (body) => {
        const msg = body.querySelector("#s-msg");
        const bindResult = body.querySelector("#s-bind-result");
        body.querySelector("#s-bind")?.addEventListener("click", async () => {
          const server = body.querySelector("#s-server").value.trim();
          const ca = body.querySelector("#s-ca").value.trim();
          const code = body.querySelector("#s-code").value.trim();
          if (!server) {
            bindResult.className = "msg";
            bindResult.textContent = "\u8BF7\u8F93\u5165\u670D\u52A1\u5668\u5730\u5740";
            return;
          }
          if (!code) {
            bindResult.className = "msg";
            bindResult.textContent = "\u8BF7\u8F93\u5165\u8BBE\u5907\u6388\u6743\u7801";
            return;
          }
          bindResult.className = "msg";
          bindResult.textContent = "\u6B63\u5728\u94FE\u63A5\u2026";
          try {
            await backend.bind({ server, ca, code });
            saveSettings({ ...loadSettings(), server, ca });
            bindResult.className = "msg ok";
            bindResult.textContent = `\u5DF2\u7ED1\u5B9A ${server}`;
            toast("\u5DF2\u7ED1\u5B9A\u670D\u52A1\u5668");
            skipOnboarding = true;
            await refresh();
            skipOnboarding = false;
            closeModal();
          } catch (e) {
            bindResult.className = "msg";
            bindResult.textContent = `\u94FE\u63A5\u5931\u8D25: ${e}`;
          }
        });
        body.querySelector("#s-start-daemon")?.addEventListener("click", async () => {
          msg.className = "msg";
          msg.textContent = "\u6B63\u5728\u542F\u52A8\u2026";
          try {
            await backend.ensureDaemon();
            msg.className = "msg ok";
            msg.textContent = "\u540E\u53F0\u670D\u52A1\u5DF2\u5C31\u7EEA";
            await refresh();
            const row = body.querySelector("#s-daemon-row");
            if (row) row.innerHTML = '<span class="live-dot ok">\u540E\u53F0\u670D\u52A1: \u8FD0\u884C\u4E2D</span>';
          } catch (e) {
            msg.className = "msg";
            msg.textContent = String(e);
          }
        });
      }
    });
  }
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
    const serverInput = $("#onb-server");
    const caInput = $("#onb-ca");
    if (serverInput) serverInput.value = s.server;
    if (caInput) caInput.value = s.ca;
    const daemonSection = $("#onb-daemon");
    if (daemonSection) daemonSection.hidden = !!status;
    const startDaemon = $("#onb-start-daemon");
    if (startDaemon) {
      startDaemon.onclick = async () => {
        startDaemon.disabled = true;
        startDaemon.textContent = "\u542F\u52A8\u4E2D\u2026";
        const daemonResult = $("#onb-daemon-result");
        daemonResult.className = "msg";
        daemonResult.textContent = "\u6B63\u5728\u542F\u52A8\u2026";
        try {
          await backend.ensureDaemon();
          daemonResult.className = "msg ok";
          daemonResult.textContent = "\u540E\u53F0\u670D\u52A1\u5DF2\u5C31\u7EEA";
          await refresh();
          if (daemonSection) daemonSection.hidden = true;
        } catch (e) {
          daemonResult.className = "msg";
          daemonResult.textContent = `\u542F\u52A8\u5931\u8D25: ${e}`;
        } finally {
          startDaemon.disabled = false;
          startDaemon.textContent = "\u542F\u52A8";
        }
      };
    }
    const bindResult = $("#onb-bind-result");
    $("#onb-bind").onclick = async () => {
      const server = $("#onb-server").value.trim();
      const ca = $("#onb-ca").value.trim();
      const code = $("#onb-code").value.trim();
      if (!server) {
        bindResult.className = "msg";
        bindResult.textContent = "\u8BF7\u8F93\u5165\u670D\u52A1\u5668\u5730\u5740";
        return;
      }
      if (!code) {
        bindResult.className = "msg";
        bindResult.textContent = "\u8BF7\u8F93\u5165\u8BBE\u5907\u6388\u6743\u7801";
        return;
      }
      bindResult.className = "msg";
      bindResult.textContent = "\u6B63\u5728\u7ED1\u5B9A\u2026";
      try {
        await backend.bind({ server, ca, code });
        saveSettings({ ...loadSettings(), server, ca });
        bindResult.className = "msg ok";
        bindResult.textContent = `\u5DF2\u7ED1\u5B9A ${server}`;
        toast("\u5DF2\u7ED1\u5B9A\u670D\u52A1\u5668");
        await finishOnboarding();
      } catch (e) {
        bindResult.className = "msg";
        bindResult.textContent = `\u7ED1\u5B9A\u5931\u8D25: ${e}`;
      }
    };
    $("#onb-skip").onclick = (e) => {
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
      if (tunnelDetail) tunnelDetail.innerHTML = `<p class="muted">\u540E\u53F0\u670D\u52A1\u672A\u8FD0\u884C</p>`;
      if (statusJson) statusJson.textContent = "(\u672A\u8FDE\u63A5\u540E\u53F0\u670D\u52A1)";
      return;
    }
    if (deviceId) deviceId.textContent = status.deviceId || "-";
    const addr = status.serverAddr ?? "";
    if (svcServer) svcServer.textContent = status.bound && addr ? addr : "\u672A\u8FDE\u63A5\u670D\u52A1\u5668";
    if (svcWgport) svcWgport.textContent = String(status.wgPort ?? "-");
    const nets = status.networks ?? [];
    if (tunnelDetail) {
      tunnelDetail.innerHTML = nets.length ? nets.map((n) => {
        const stats = Object.values(n.peerStats ?? {});
        const bytes = stats.reduce((a, p) => a + (p.RxBytes ?? 0) + (p.TxBytes ?? 0), 0);
        const peers = stats.length;
        const linked = !!n.interface;
        return `<div class="kv"><span>${esc(n.name || n.networkId)}</span>
            <code>${esc(n.interface || "\u65E0\u96A7\u9053")}</code>
            <span class="muted">\u6536/\u53D1 ${fmtBytes(bytes)} \xB7 \u6210\u5458 ${peers ? `${onlineCount(n)}/${peers} \u5728\u7EBF` : "\u6682\u65E0\u5176\u4ED6\u6210\u5458"}</span>
            ${linked ? '<span class="pill ok">\u5DF2\u94FE\u63A5</span>' : '<span class="pill off">\u672A\u94FE\u63A5</span>'}
          </div>`;
      }).join("") : `<p class="muted">\u672A\u52A0\u5165\u4EFB\u4F55\u7F51\u7EDC</p>`;
    }
    if (statusJson) statusJson.textContent = JSON.stringify(status, null, 2);
  }
  async function ensureDaemon() {
    const btn = $("#header-svc-btn");
    if (btn) {
      btn.disabled = true;
      btn.textContent = "\u542F\u52A8\u4E2D\u2026";
    }
    try {
      await backend.ensureDaemon();
      toast("\u540E\u53F0\u670D\u52A1\u5DF2\u5C31\u7EEA");
    } catch (e) {
      toast(String(e), "err");
    } finally {
      if (btn) btn.disabled = false;
      await refresh();
    }
  }
  function bindEvents() {
    $("#btn-settings")?.addEventListener("click", openSettingsModal);
    $("#btn-create")?.addEventListener("click", openCreateModal);
    $("#btn-join")?.addEventListener("click", openJoinModal);
    $("#btn-refresh")?.addEventListener("click", () => void refresh());
    $("#btn-leave-all")?.addEventListener("click", async () => {
      try {
        await backend.leave("");
        toast("\u5DF2\u505C\u6B62\u5168\u90E8\u7F51\u7EDC");
      } catch (e) {
        toast(String(e), "err");
      }
      await refresh();
    });
    const btnMore = $("#btn-more");
    const dropdown = $("#header-dropdown");
    if (btnMore && dropdown) {
      btnMore.addEventListener("click", (e) => {
        e.stopPropagation();
        dropdown.hidden = !dropdown.hidden;
      });
      document.addEventListener("click", () => {
        dropdown.hidden = true;
      });
      dropdown.querySelectorAll("[data-action]").forEach((item) => {
        item.addEventListener("click", () => {
          dropdown.hidden = true;
          const action = item.dataset.action;
          if (action === "refresh") void refresh();
          else if (action === "settings") openSettingsModal();
          else if (action === "logout") {
            window.dispatchEvent(new Event("snet-logout"));
          }
        });
      });
    }
  }
  async function init(b) {
    backend = b;
    await loadQR();
    initTabs();
    bindNetListEvents();
    bindEvents();
    if (backend.hasDaemonControl) {
      const lo = $("#btn-logout");
      if (lo) lo.style.display = "none";
      document.querySelectorAll('[data-action="logout"]').forEach((el) => {
        el.style.display = "none";
      });
    }
    refresh();
    setInterval(() => {
      if (!document.hidden) refresh();
    }, 3e3);
  }

  // android/src/android.ts
  function call(fn, ...args) {
    const result = window.WebBridge[fn](...args);
    return JSON.parse(result);
  }
  var backend2 = {
    hasDaemonControl: true,
    async status() {
      try {
        const raw = window.WebBridge.status();
        return JSON.parse(raw);
      } catch {
        return null;
      }
    },
    async create(params) {
      return call("create", JSON.stringify({
        server: params.server,
        port: params.port,
        ca: params.ca,
        name: params.name,
        subnet: params.subnet,
        approvalRequired: params.approvalRequired
      }));
    },
    async join(params) {
      return call("join", JSON.stringify({
        server: params.server,
        port: params.port,
        ca: params.ca,
        link: params.link
      }));
    },
    async bind(params) {
      call("bind", JSON.stringify({
        server: params.server,
        ca: params.ca,
        code: params.code
      }));
    },
    async rejoin(nid) {
      call("rejoin", nid);
    },
    async leave(nid) {
      call("leave", nid);
    },
    async remove(nid) {
      call("remove", nid);
    },
    async deleteNet(nid) {
      call("deleteNet", nid);
    },
    async netinfo(nid) {
      return call("netinfo", nid);
    },
    async peers(nid) {
      return call("peers", nid);
    },
    async updateSettings(params) {
      call("updateSettings", JSON.stringify({
        nid: params.nid,
        name: params.name,
        subnet: params.subnet,
        approvalRequired: params.approvalRequired
      }));
    },
    async updateSubnets(params) {
      call("updateSubnets", JSON.stringify({
        nid: params.nid,
        subnets: JSON.stringify(params.subnets)
      }));
    },
    async kick(params) {
      call("kick", JSON.stringify({
        nid: params.nid,
        nodeId: params.nodeId
      }));
    },
    async approve(params) {
      call("approve", JSON.stringify({
        nid: params.nid,
        pendingId: params.pendingId
      }));
    },
    async deny(params) {
      call("deny", JSON.stringify({
        nid: params.nid,
        pendingId: params.pendingId
      }));
    },
    async cancelPending(pendingId) {
      call("cancelPending", pendingId);
    },
    async resetCode(nid) {
      return call("resetCode", nid);
    },
    async detectLocalSubnets() {
      return call("detectLocalSubnets");
    },
    async ensureDaemon() {
      call("ensureDaemon");
    }
  };
  function showApp() {
    const app = document.getElementById("app");
    const login = document.getElementById("login-page");
    const setup = document.getElementById("setup-page");
    const onb = document.getElementById("onboarding-page");
    if (app) app.hidden = false;
    if (login) login.hidden = true;
    if (setup) setup.hidden = true;
    if (onb) onb.hidden = true;
  }
  function boot() {
    showApp();
    init(backend2);
    document.getElementById("btn-logout")?.addEventListener("click", () => {
      if (confirm("\u786E\u5B9A\u9000\u51FA\u5F53\u524D\u8BBE\u5907\u7ED1\u5B9A\uFF1F")) window.location.reload();
    });
    window.addEventListener("snet-logout", () => {
      if (confirm("\u786E\u5B9A\u9000\u51FA\u5F53\u524D\u8BBE\u5907\u7ED1\u5B9A\uFF1F")) window.location.reload();
    });
  }
  if (document.readyState === "loading") {
    window.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
