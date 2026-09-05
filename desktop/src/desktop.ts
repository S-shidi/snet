/* ── Desktop adapter: Tauri IPC → shared UI ────────────────────── */
import { invoke } from "@tauri-apps/api/core";
import type { Backend, DaemonStatus, NetInfoDetail, PeersResp, CreateResp, JoinResp } from "../../shared/web/types.js";
import { init } from "../../shared/web/ui.js";

const call = <T>(cmd: string, args?: Record<string, unknown>): Promise<T> => invoke<T>(cmd, args);

const backend: Backend = {
  hasDaemonControl: true,

  async status(): Promise<DaemonStatus | null> {
    try {
      return await call<DaemonStatus>("daemon_status");
    } catch {
      return null;
    }
  },

  async create(params: { server: string; port: number; ca: string; name: string; subnet: string; approvalRequired: boolean; description?: string; tags?: string[]; visibility?: string }): Promise<CreateResp> {
    return call<CreateResp>("create_network", {
      server: params.server,
      port: params.port,
      ca: params.ca,
      name: params.name,
      subnet: params.subnet,
      approvalRequired: params.approvalRequired,
      description: params.description ?? "",
      tags: params.tags ?? [],
      visibility: params.visibility ?? "",
    });
  },

  async join(params: { server: string; port: number; ca: string; link: string }): Promise<JoinResp> {
    return call<JoinResp>("join_network", {
      server: params.server,
      port: params.port,
      ca: params.ca,
      link: params.link,
    });
  },

  async bind(params: { server: string; ca: string; code: string }): Promise<void> {
    await call("bind_server", { server: params.server, ca: params.ca, code: params.code });
  },

  async rejoin(nid: string): Promise<void> { await call("rejoin_nid", { nid }); },
  async leave(nid: string): Promise<void> { await call("leave_nid", { nid }); },
  async remove(nid: string): Promise<void> { await call("remove_nid", { nid }); },
  async deleteNet(nid: string): Promise<void> { await call("delete_nid", { nid }); },

  async netinfo(nid: string): Promise<NetInfoDetail> {
    return call<NetInfoDetail>("netinfo", { nid });
  },

  async peers(nid: string): Promise<PeersResp> {
    return call<PeersResp>("peers", { nid });
  },

  async updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null; description?: string; tags?: string[]; visibility?: string }): Promise<void> {
    await call("update_settings", {
      nid: params.nid,
      name: params.name,
      subnet: params.subnet,
      approvalRequired: params.approvalRequired,
      description: params.description ?? "",
      tags: params.tags ?? [],
      visibility: params.visibility ?? "",
    });
  },

  async setRole(params: { nid: string; nodeId: string; role: string }): Promise<void> {
    await call("set_role", { nid: params.nid, nodeId: params.nodeId, role: params.role });
  },

  async updateSubnets(params: { nid: string; subnets: string[] }): Promise<void> {
    await call("update_subnets", { nid: params.nid, subnets: params.subnets });
  },

  async kick(params: { nid: string; nodeId: string }): Promise<void> {
    await call("kick_nid", { nid: params.nid, nodeId: params.nodeId });
  },

  async approve(params: { nid: string; pendingId: string }): Promise<void> {
    await call("approve_pending", { nid: params.nid, pendingId: params.pendingId });
  },

  async deny(params: { nid: string; pendingId: string }): Promise<void> {
    await call("deny_pending", { nid: params.nid, pendingId: params.pendingId });
  },

  async cancelPending(pendingId: string): Promise<void> {
    await call("cancel_pending", { pendingId });
  },

  async resetCode(nid: string): Promise<{ pairingCode: string }> {
    return call<{ pairingCode: string }>("reset_code", { nid });
  },

  async detectLocalSubnets(): Promise<string[]> {
    return call<string[]>("detect_local_subnets");
  },

  async ensureDaemon(): Promise<void> {
    await call("ensure_daemon");
  },
};

init(backend);
