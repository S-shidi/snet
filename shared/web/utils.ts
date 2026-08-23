/* ── Shared UI utilities ────────────────────────────────────────── */

const $ = <T extends HTMLElement>(sel: string): T => document.querySelector<T>(sel)!;

export { $ };

export const esc = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

export const SPIN = '<span class="spinner"></span>';

/* ── QR Code (local, no external API) ─────────────────────────── */
let qrcodeFn: ((typeNumber: number, errorCorrectionLevel: string) => { addData(data: string): void; make(): void; createSvgTag(opts: { cellSize: number; margin: number; scalable: boolean }): string }) | null = null;

export async function loadQR() {
  if (qrcodeFn) return;
  try {
    const mod = await import("qrcode-generator");
    qrcodeFn = mod.default;
  } catch {
    // Web build bundles qrcode-generator statically; desktop uses dynamic import
  }
}

export function renderQR(container: HTMLElement, text: string) {
  container.innerHTML = "";
  if (qrcodeFn) {
    const qr = qrcodeFn(0, "M");
    qr.addData(text);
    qr.make();
    const svg = qr.createSvgTag({ cellSize: 4, margin: 1, scalable: true });
    container.innerHTML = svg;
    container.querySelector("svg")?.setAttribute("style", "width:100%;height:auto;display:block");
  } else {
    // Fallback: show link text
    container.innerHTML = `<p style="color:var(--dim);font-size:12px;text-align:center">${esc(text)}</p>`;
  }
}

/* ── Format bytes ─────────────────────────────────────────────── */
export function fmtBytes(b?: number): string {
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

/* ── Online count (unified: handshake window) ─────────────────── */
const ONLINE_WINDOW_SEC = 180;
function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}
export function onlineCount(n: { peerStats?: Record<string, { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number }> }): number {
  const stats = n.peerStats ?? {};
  const now = nowSec();
  return Object.values(stats).filter((p) => p.LastHandshakeSec && now - p.LastHandshakeSec < ONLINE_WINDOW_SEC).length;
}

/* ── Toast ────────────────────────────────────────────────────── */
export function toast(msg: string, kind: "ok" | "err" | "warn" = "ok") {
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

/* ── Clipboard ────────────────────────────────────────────────── */
export async function copyText(text: string, btn?: HTMLElement | null) {
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

/* ── Modal framework ──────────────────────────────────────────── */
const modalRoot = $("#modal-root");
let modalOnClose: (() => void) | null = null;

export type ModalOpts = {
  title: string;
  body: string;
  footer?: string;
  wide?: boolean;
  onBody?: (b: HTMLElement) => void;
  onSave?: (b: HTMLElement) => void;
  onClose?: () => void;
};

export function openModal(opts: ModalOpts) {
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

export function closeModal() {
  const cb = modalOnClose;
  modalOnClose = null;
  modalRoot.hidden = true;
  modalRoot.innerHTML = "";
  cb?.();
}

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeModal();
});

/* ── Confirm dialog ───────────────────────────────────────────── */
export function confirmDialog(title: string, message: string, danger = false): Promise<boolean> {
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

/* ── Tab switching + sliding pill ──────────────────────────────── */
export function initTabs() {
  const tabs = Array.from(document.querySelectorAll<HTMLElement>(".tab"));
  const slider = document.getElementById("tabs-slider") as HTMLElement | null;

  function updateSlider() {
    const active = document.querySelector<HTMLElement>(".tab.active");
    if (!active || !slider) return;
    const bar = active.parentElement as HTMLElement;
    const barRect = bar.getBoundingClientRect();
    const tabRect = active.getBoundingClientRect();
    const left = tabRect.left - barRect.left;
    slider.style.left = `${left}px`;
    slider.style.width = `${tabRect.width}px`;
  }

  tabs.forEach((t) =>
    t.addEventListener("click", () => {
      tabs.forEach((x) => x.classList.toggle("active", x === t));
      const name = t.dataset.tab!;
      document.querySelectorAll(".tab-panel").forEach((p) =>
        p.classList.toggle("active", p.id === `tab-${name}` || p.id === `panel-${name}`),
      );
      updateSlider();
    }),
  );

  // Initial slider position
  requestAnimationFrame(updateSlider);
  window.addEventListener("resize", updateSlider);
}
