/* ── Shared subnet validation widget ──────────────────────────────
 * Rules mirror server-side internal/server/store.go validateSubnet:
 * IPv4 private ranges only (10/8, 172.16/12, 192.168/16),
 * prefix /8–/24, host bits must be zero.
 * Keep in sync with internal/server/admin.html and internal/server/store.go.
 */
import { esc } from "./utils.js";

export type SubnetCheck =
  | { ok: true; value: string; fix?: boolean; text: string }
  | { ok: false; error: string };

export const SUBNET_PREFIXES = [24, 23, 22, 20, 16, 12, 8];
export const SUBNET_CHIPS = ["10.88.0.0/24", "10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"];

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

export function subnetCheck(raw: string, selectPrefix: number): SubnetCheck {
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

export type SubnetWidget = {
  check: () => SubnetCheck;
  get: () => string;
  render: () => void;
};

export function subnetWidgetHTML(opts: { id?: string; value?: string; placeholder?: string; emptyHint?: string }): string {
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

export function subnetWidgetInit(wrap: HTMLElement): SubnetWidget {
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
