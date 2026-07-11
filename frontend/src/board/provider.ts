import * as Y from 'yjs';
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate, removeAwarenessStates } from 'y-protocols/awareness';
import { api, wsURL } from '../lib/api';
import type { ConnectionState, RemotePresence, Viewport } from './store';

interface WireMessage {
  type: string;
  board_id?: string;
  client_id?: string;
  user_id?: string;
  can_edit?: boolean;
  update_id?: string;
  server_sequence?: number;
  through_sequence?: number;
  request_id?: string;
  duplicate?: boolean;
  data?: string;
  removed?: boolean;
  code?: string;
  message?: string;
}

interface ProviderCallbacks {
  onConnection: (state: ConnectionState, sequence?: number) => void;
  onPending: (count: number) => void;
  onError: (message: string) => void;
  onPresence: (presence: Record<number, RemotePresence>) => void;
}

export class BoardProvider {
  readonly awareness: Awareness;
  readonly clientID = `cli_${crypto.randomUUID()}`;
  private socket: WebSocket | null = null;
  private stopped = false;
  private synced = false;
  private reconnectAttempt = 0;
  private reconnectTimer = 0;
  private pending = new Map<string, Uint8Array>();
  private remoteAwarenessIDs = new Map<string, Set<number>>();
  private authoritativeCanEdit: boolean;

  constructor(
    private readonly boardID: string,
    private readonly doc: Y.Doc,
    canEdit: boolean,
    private readonly callbacks: ProviderCallbacks
  ) {
    this.authoritativeCanEdit = canEdit;
    this.awareness = new Awareness(doc);
    doc.on('update', this.handleDocumentUpdate);
    this.awareness.on('update', this.handleAwarenessUpdate);
    this.awareness.on('change', this.publishPresence);
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    window.clearTimeout(this.reconnectTimer);
    this.awareness.setLocalState(null);
    this.socket?.close(1000, 'page closed');
    this.socket = null;
    this.doc.off('update', this.handleDocumentUpdate);
    this.awareness.off('update', this.handleAwarenessUpdate);
    this.awareness.off('change', this.publishPresence);
    this.awareness.destroy();
  }

  setLocalPresence(input: { user: { id: string; name: string; color: string }; cursor?: { x: number; y: number }; viewport: Viewport; selection: string[] }) {
    this.awareness.setLocalState(input);
  }

  updateLocalPresence(field: 'cursor' | 'viewport' | 'selection', value: unknown) {
    this.awareness.setLocalStateField(field, value);
  }

  private connect() {
    if (this.stopped) return;
    this.callbacks.onConnection(this.reconnectAttempt ? 'offline' : 'connecting');
    const socket = new WebSocket(wsURL(this.boardID, this.clientID));
    this.socket = socket;
    this.synced = false;
    socket.onopen = () => {
      this.reconnectAttempt = 0;
      this.callbacks.onConnection('syncing');
    };
    socket.onmessage = (event) => this.receive(event.data);
    socket.onerror = () => socket.close();
    socket.onclose = () => {
      if (this.socket === socket) this.socket = null;
      if (this.stopped) return;
      this.callbacks.onConnection('offline');
      void api('/api/me').catch(() => undefined);
      this.scheduleReconnect();
    };
  }

  private receive(raw: unknown) {
    if (typeof raw !== 'string') return;
    let message: WireMessage;
    try { message = JSON.parse(raw) as WireMessage; } catch { return; }
    switch (message.type) {
      case 'sync_start':
        this.authoritativeCanEdit = message.can_edit === true;
        this.callbacks.onConnection('syncing', message.server_sequence);
        break;
      case 'checkpoint':
      case 'update':
        if (message.data) Y.applyUpdate(this.doc, fromBase64(message.data), this);
        if (message.type === 'update' && message.server_sequence !== undefined) this.callbacks.onConnection(this.synced ? 'live' : 'syncing', message.server_sequence);
        break;
      case 'sync_complete':
        this.synced = true;
        this.callbacks.onConnection('live', message.server_sequence);
        this.sendPending();
        this.sendLocalAwareness();
        break;
      case 'update_ack':
        if (message.update_id) this.pending.delete(message.update_id);
        this.callbacks.onPending(this.pending.size);
        if (message.server_sequence !== undefined) this.callbacks.onConnection('live', message.server_sequence);
        break;
      case 'awareness':
        this.receiveAwareness(message);
        break;
      case 'checkpoint_request':
        if (this.authoritativeCanEdit && message.request_id && message.through_sequence !== undefined) {
          this.send({
            type: 'checkpoint', request_id: message.request_id, through_sequence: message.through_sequence,
            data: toBase64(Y.encodeStateAsUpdate(this.doc))
          });
        }
        break;
      case 'error':
        this.callbacks.onError(message.message ?? message.code ?? 'Collaboration error');
        if (message.update_id && (message.code === 'forbidden' || message.code === 'invalid_update')) {
          this.pending.delete(message.update_id);
          this.callbacks.onPending(this.pending.size);
        }
        break;
    }
  }

  private receiveAwareness(message: WireMessage) {
    if (message.removed && message.client_id) {
      const ids = Array.from(this.remoteAwarenessIDs.get(message.client_id) ?? []);
      if (ids.length) removeAwarenessStates(this.awareness, ids, this);
      this.remoteAwarenessIDs.delete(message.client_id);
      return;
    }
    if (!message.data) return;
    const before = new Set(this.awareness.getStates().keys());
    applyAwarenessUpdate(this.awareness, fromBase64(message.data), this);
    if (message.client_id) {
      const known = this.remoteAwarenessIDs.get(message.client_id) ?? new Set<number>();
      for (const id of this.awareness.getStates().keys()) if (!before.has(id) && id !== this.awareness.clientID) known.add(id);
      this.remoteAwarenessIDs.set(message.client_id, known);
    }
  }

  private handleDocumentUpdate = (update: Uint8Array, origin: unknown) => {
    if (origin === this || !this.authoritativeCanEdit) return;
    const updateID = `upd_${crypto.randomUUID()}`;
    this.pending.set(updateID, update.slice());
    this.callbacks.onPending(this.pending.size);
    if (this.synced) this.sendUpdate(updateID, update);
  };

  private handleAwarenessUpdate = ({ added, updated, removed }: { added: number[]; updated: number[]; removed: number[] }, origin: unknown) => {
    if (origin === this || !this.synced) return;
    const clients = added.concat(updated, removed).filter((id) => id === this.awareness.clientID);
    if (clients.length) this.send({ type: 'awareness', data: toBase64(encodeAwarenessUpdate(this.awareness, clients)) });
  };

  private publishPresence = () => {
    const presence: Record<number, RemotePresence> = {};
    this.awareness.getStates().forEach((raw, awarenessID) => {
      if (awarenessID === this.awareness.clientID) return;
      const state = raw as { user?: { id?: unknown; name?: unknown; color?: unknown }; cursor?: unknown; viewport?: unknown; selection?: unknown };
      if (!state.user || typeof state.user.id !== 'string') return;
      presence[awarenessID] = {
        awarenessID,
        userID: state.user.id,
        name: typeof state.user.name === 'string' ? state.user.name : 'Collaborator',
        color: typeof state.user.color === 'string' ? state.user.color : '#2563eb',
        cursor: point(state.cursor),
        viewport: viewport(state.viewport),
        selection: Array.isArray(state.selection) ? state.selection.filter((id): id is string => typeof id === 'string') : []
      };
    });
    this.callbacks.onPresence(presence);
  };

  private sendPending() {
    for (const [id, update] of this.pending) this.sendUpdate(id, update);
  }

  private sendUpdate(id: string, update: Uint8Array) {
    this.send({ type: 'update', update_id: id, data: toBase64(update) });
  }

  private sendLocalAwareness() {
    if (this.awareness.getLocalState()) {
      this.send({ type: 'awareness', data: toBase64(encodeAwarenessUpdate(this.awareness, [this.awareness.clientID])) });
    }
  }

  private send(message: Record<string, unknown>) {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(message));
  }

  private scheduleReconnect() {
    window.clearTimeout(this.reconnectTimer);
    const delay = Math.min(15_000, 500 * 2 ** Math.min(this.reconnectAttempt, 5)) + Math.random() * 300;
    this.reconnectAttempt += 1;
    this.reconnectTimer = window.setTimeout(() => this.connect(), delay);
  }
}

function toBase64(bytes: Uint8Array) {
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  return btoa(binary);
}

function fromBase64(value: string) {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function point(value: unknown) {
  if (!value || typeof value !== 'object') return undefined;
  const candidate = value as { x?: unknown; y?: unknown };
  return typeof candidate.x === 'number' && typeof candidate.y === 'number' ? { x: candidate.x, y: candidate.y } : undefined;
}

function viewport(value: unknown): Viewport | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const candidate = value as { x?: unknown; y?: unknown; scale?: unknown };
  return typeof candidate.x === 'number' && typeof candidate.y === 'number' && typeof candidate.scale === 'number' ? { x: candidate.x, y: candidate.y, scale: candidate.scale } : undefined;
}
