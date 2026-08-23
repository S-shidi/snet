/* ── Shared types for SNET client UI ────────────────────────────── */

export type PeerStats = { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number };
export type NetInfo = {
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
  allowedSubnets?: string[];
};
export type PendingJoin = {
  pendingId: string;
  networkId: string;
  createdAt?: string;
  status?: string;
  error?: string;
};
export type DaemonStatus = {
  deviceId?: string;
  serverAddr?: string;
  bound?: boolean;
  wgPort?: number;
  networks?: NetInfo[];
  pendingJoins?: PendingJoin[];
};
export type NetInfoDetail = {
  id?: string;
  networkId?: string;
  name?: string;
  subnet?: string;
  approvalRequired?: boolean;
  pendingCount?: number;
  nodeCount?: number;
  pairingCode?: string;
  pending?: Array<{ id: string; publicKey: string; deviceId?: string; createdAt?: string }>;
  nodes?: Array<{ id: string; ip: string; deviceId?: string; online?: boolean; allowedSubnets?: string[] }>;
};
export type NetNode = { id: string; ip: string; publicKey?: string; deviceId?: string; online?: boolean; allowedSubnets?: string[] };
export type PeersResp = { peers?: NetNode[]; self?: NetNode; subnet?: string; approvalRequired?: boolean };
export type CreateResp = { networkId: string; ip: string; pairingCode: string; link: string };
export type JoinResp = { ip?: string; networkId?: string; status?: string; pendingId?: string };

/* ── Backend interface: platform adapters implement this ──────── */
export interface Backend {
  /** Get daemon status (returns null on failure) */
  status(): Promise<DaemonStatus | null>;
  /** Create a network */
  create(params: { server: string; port: number; ca: string; name: string; subnet: string; approvalRequired: boolean }): Promise<CreateResp>;
  /** Join a network */
  join(params: { server: string; port: number; ca: string; link: string }): Promise<JoinResp>;
  /** Bind device to server */
  bind(params: { server: string; ca: string; code: string }): Promise<void>;
  /** Rejoin (connect) a network */
  rejoin(nid: string): Promise<void>;
  /** Leave (disconnect) a network */
  leave(nid: string): Promise<void>;
  /** Remove network config from device */
  remove(nid: string): Promise<void>;
  /** Delete network from server (owner only) */
  deleteNet(nid: string): Promise<void>;
  /** Get network info (owner detail) */
  netinfo(nid: string): Promise<NetInfoDetail>;
  /** Get peers for a network */
  peers(nid: string): Promise<PeersResp>;
  /** Update network settings (owner only) */
  updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null }): Promise<void>;
  /** Update subnets for a network */
  updateSubnets(params: { nid: string; subnets: string[] }): Promise<void>;
  /** Kick a node from a network */
  kick(params: { nid: string; nodeId: string }): Promise<void>;
  /** Approve a pending join request */
  approve(params: { nid: string; pendingId: string }): Promise<void>;
  /** Deny a pending join request */
  deny(params: { nid: string; pendingId: string }): Promise<void>;
  /** Cancel a pending join request */
  cancelPending(pendingId: string): Promise<void>;
  /** Reset pairing code */
  resetCode(nid: string): Promise<{ pairingCode: string }>;
  /** Detect local subnets */
  detectLocalSubnets(): Promise<string[]>;
  /** Ensure daemon is running (Desktop only, Web returns error) */
  ensureDaemon(): Promise<void>;
  /** Whether the backend supports daemon lifecycle management */
  hasDaemonControl: boolean;
}
