import * as Y from 'yjs';
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate, removeAwarenessStates } from 'y-protocols/awareness';
import { APIError, api, wsURL } from '../lib/api';
import { introducedAssetIDs, referencedAssetIDs, referencedAssetsByBlock } from './schema';
import type { ConnectionState, RemotePresence, Viewport } from './store';

export const COLLABORATION_PROTOCOL_VERSION = 4;

interface WireMessage {
  type: string;
  protocol?: number;
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
  onPermission: (canEdit: boolean) => void;
  onResetRequired: () => void;
}

interface PendingUpdate {
  data: Uint8Array;
  referenceBaseSequence: number;
  assetIDs: string[];
  introducedAssetIDs: string[];
}

export class BoardProvider {
  readonly awareness: Awareness;
  readonly clientID = `cli_${crypto.randomUUID()}`;
  private socket: WebSocket | null = null;
  private stopped = false;
  private synced = false;
  private reconnectAttempt = 0;
  private reconnectTimer = 0;
  private referenceTimer = 0;
  private referencesInFlight = false;
  private referenceRetryAttempt = 0;
  private sequenceFrontier = 0;
  private latestKnownSequence = 0;
  private seenSequences = new Set<number>();
  private pending = new Map<string, PendingUpdate>();
  private lastAssetsByBlock: Map<string, string>;
  private remoteAwarenessIDs = new Map<string, Set<number>>();
  private authoritativeCanEdit: boolean;

  constructor(
    private readonly boardID: string,
    private readonly doc: Y.Doc,
    canEdit: boolean,
    private canPublishAssetReferences: boolean,
    private readonly callbacks: ProviderCallbacks
  ) {
    this.authoritativeCanEdit = canEdit;
    this.lastAssetsByBlock = referencedAssetsByBlock(doc);
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
    window.clearTimeout(this.referenceTimer);
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
        if (message.protocol !== COLLABORATION_PROTOCOL_VERSION) {
          this.stopped = true;
          this.setAuthoritativePermission(false);
          this.callbacks.onError(`Unsupported collaboration protocol ${message.protocol ?? 'unknown'}; expected v${COLLABORATION_PROTOCOL_VERSION}`);
          this.callbacks.onConnection('offline');
          this.socket?.close(1002, 'unsupported protocol');
          return;
        }
        this.setAuthoritativePermission(message.can_edit === true);
        if (!this.authoritativeCanEdit && this.pending.size > 0) {
          this.discardPendingAndReset();
          return;
        }
        this.callbacks.onConnection('syncing', message.server_sequence);
        break;
      case 'checkpoint':
      case 'update':
        const applyError = message.data ? this.applyServerUpdate(message.data) : undefined;
        if (applyError && message.type === 'checkpoint') {
          this.callbacks.onError(`Stored board checkpoint is invalid: ${applyError}`);
          this.stopped = true;
          this.callbacks.onConnection('offline');
          this.socket?.close(1003, 'invalid checkpoint');
          return;
        }
        if (message.server_sequence !== undefined) {
          if (message.type === 'checkpoint') this.applyCheckpointSequence(message.server_sequence);
          else this.observeSequence(message.server_sequence);
        }
        if (message.type === 'update' && message.server_sequence !== undefined) {
          this.callbacks.onConnection(this.synced ? 'live' : 'syncing', message.server_sequence);
          this.scheduleReferenceSync();
        }
        if (applyError) this.callbacks.onError(`Ignored invalid document update at sequence ${message.server_sequence ?? 'unknown'}: ${applyError}`);
        break;
      case 'sync_complete':
        if (message.server_sequence !== undefined && message.server_sequence < this.sequenceFrontier) {
          this.callbacks.onError('The server document was restored to an earlier sequence; reloading authoritative state');
          this.discardPendingAndReset();
          return;
        }
        if (message.server_sequence !== undefined) this.latestKnownSequence = Math.max(this.latestKnownSequence, message.server_sequence);
        if (message.server_sequence !== undefined && this.sequenceFrontier < message.server_sequence) {
          this.callbacks.onError('Collaboration sync ended before all ordered updates were applied');
          this.socket?.close(1011, 'incomplete ordered sync');
          return;
        }
        this.synced = true;
        this.callbacks.onConnection('live', message.server_sequence);
        this.sendPending();
        this.sendLocalAwareness();
        this.scheduleReferenceSync();
        break;
      case 'update_ack':
        if (message.update_id) this.pending.delete(message.update_id);
        if (message.server_sequence !== undefined) this.observeSequence(message.server_sequence);
        this.callbacks.onPending(this.pending.size);
        if (message.server_sequence !== undefined) this.callbacks.onConnection('live', message.server_sequence);
        this.scheduleReferenceSync();
        break;
      case 'awareness':
        this.receiveAwareness(message);
        break;
      case 'checkpoint_request':
        if (this.authoritativeCanEdit && this.canPublishAssetReferences && message.request_id && message.through_sequence !== undefined) {
          this.send({
            type: 'checkpoint', request_id: message.request_id, through_sequence: message.through_sequence,
            data: toBase64(Y.encodeStateAsUpdate(this.doc))
          });
        }
        break;
      case 'error':
        this.callbacks.onError(message.message ?? message.code ?? 'Collaboration error');
        if (message.code === 'forbidden') {
          this.setAuthoritativePermission(false);
          this.discardPendingAndReset();
          return;
        }
        if (message.update_id && ['invalid_update', 'invalid_asset_reference', 'asset_claims_required', 'update_id_conflict'].includes(message.code ?? '')) {
          this.discardPendingAndReset();
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
    const assetsByBlock = referencedAssetsByBlock(this.doc);
    const introducedAssets = introducedAssetIDs(this.lastAssetsByBlock, assetsByBlock);
    this.lastAssetsByBlock = assetsByBlock;
    const assetIDs = Array.from(new Set(assetsByBlock.values())).sort();
    if (origin === this || !this.authoritativeCanEdit) return;
    const updateID = `upd_${crypto.randomUUID()}`;
    const pending: PendingUpdate = {
      data: update.slice(),
      referenceBaseSequence: this.sequenceFrontier,
      assetIDs,
      introducedAssetIDs: introducedAssets
    };
    this.pending.set(updateID, pending);
    this.callbacks.onPending(this.pending.size);
    if (this.synced) this.sendUpdate(updateID, pending);
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

  private sendUpdate(id: string, update: PendingUpdate) {
    this.send({
      type: 'update',
      update_id: id,
      data: toBase64(update.data),
      reference_base_sequence: update.referenceBaseSequence,
      asset_ids: update.assetIDs,
      introduced_asset_ids: update.introducedAssetIDs
    });
  }

  private sendLocalAwareness() {
    if (this.awareness.getLocalState()) {
      this.send({ type: 'awareness', data: toBase64(encodeAwarenessUpdate(this.awareness, [this.awareness.clientID])) });
    }
  }

  private setAuthoritativePermission(canEdit: boolean) {
    if (this.authoritativeCanEdit === canEdit) return;
    this.authoritativeCanEdit = canEdit;
    this.callbacks.onPermission(canEdit);
  }

  private discardPendingAndReset() {
    this.pending.clear();
    this.callbacks.onPending(0);
    this.socket?.close(1000, 'permission changed');
    this.callbacks.onResetRequired();
  }

  private applyCheckpointSequence(sequence: number) {
    if (!Number.isSafeInteger(sequence) || sequence < 0) return;
    this.sequenceFrontier = Math.max(this.sequenceFrontier, sequence);
    this.latestKnownSequence = Math.max(this.latestKnownSequence, sequence);
    for (const seen of this.seenSequences) if (seen <= this.sequenceFrontier) this.seenSequences.delete(seen);
    this.advanceContiguousFrontier();
  }

  private observeSequence(sequence: number) {
    if (!Number.isSafeInteger(sequence) || sequence <= 0) return;
    this.latestKnownSequence = Math.max(this.latestKnownSequence, sequence);
    if (sequence > this.sequenceFrontier) this.seenSequences.add(sequence);
    this.advanceContiguousFrontier();
  }

  private advanceContiguousFrontier() {
    while (this.seenSequences.delete(this.sequenceFrontier + 1)) this.sequenceFrontier += 1;
  }

  private scheduleReferenceSync(delay = 50) {
    if (
      this.stopped || !this.synced || !this.authoritativeCanEdit || !this.canPublishAssetReferences || this.pending.size > 0 ||
      this.sequenceFrontier !== this.latestKnownSequence
    ) return;
    window.clearTimeout(this.referenceTimer);
    this.referenceTimer = window.setTimeout(() => void this.syncReferences(), delay);
  }

  private async syncReferences() {
    if (
      this.referencesInFlight || this.stopped || !this.synced || !this.authoritativeCanEdit || !this.canPublishAssetReferences || this.pending.size > 0 ||
      this.sequenceFrontier !== this.latestKnownSequence
    ) return;
    this.referencesInFlight = true;
    const sequence = this.sequenceFrontier;
    const assetIDs = referencedAssetIDs(this.doc);
    try {
      await api(`/api/boards/${encodeURIComponent(this.boardID)}/asset-references`, {
        method: 'PUT',
        body: JSON.stringify({ through_sequence: sequence, asset_ids: assetIDs })
      });
      this.referenceRetryAttempt = 0;
      this.callbacks.onError('');
    } catch (error) {
      if (!this.stopped) {
        if (error instanceof APIError && error.status === 403) {
          this.canPublishAssetReferences = false;
        } else if (error instanceof APIError && error.code === 'asset_reference_index_stale') {
          this.scheduleReferenceSync(500);
        } else {
          this.callbacks.onError(error instanceof Error ? error.message : 'Could not synchronize image references');
          if (!(error instanceof APIError) || error.status >= 500) {
            const delay = Math.min(15_000, 500 * 2 ** Math.min(this.referenceRetryAttempt, 5));
            this.referenceRetryAttempt += 1;
            this.scheduleReferenceSync(delay);
          }
        }
      }
    } finally {
      this.referencesInFlight = false;
      if (!this.stopped && (this.sequenceFrontier !== sequence || referencedAssetIDs(this.doc).join('\u0000') !== assetIDs.join('\u0000'))) {
        this.scheduleReferenceSync();
      }
    }
  }

  private applyServerUpdate(encoded: string) {
    try {
      const update = fromBase64(encoded);
      Y.decodeUpdate(update);
      Y.applyUpdate(this.doc, update, this);
      return undefined;
    } catch (error) {
      return error instanceof Error ? error.message : 'Yjs update could not be decoded';
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
