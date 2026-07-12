import * as Y from 'yjs';
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate, removeAwarenessStates } from 'y-protocols/awareness';
import { APIError, api, wsURL } from '../lib/api';
import { createID } from '../lib/id';
import { isBatchedBoardCommandOrigin } from './commands';
import { introducedAssetIDs, referencedAssetIDs, referencedAssetsByBlock } from './schema';
import type { ConnectionState, RemotePresence, Viewport } from './store';

export const COLLABORATION_PROTOCOL_VERSION = 5;
const CLOSE_PROBE_TIMEOUT_MS = 5_000;
const DOCUMENT_UPDATE_IDLE_MS = 100;
const DOCUMENT_UPDATE_MAX_MS = 500;

interface WireMessage {
  type: string;
  protocol?: number;
  board_id?: string;
  client_id?: string;
  user_id?: string;
  user_name?: string;
  awareness_ids?: number[];
  can_edit?: boolean;
  can_manage?: boolean;
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
  onPermission: (canEdit: boolean, canManage: boolean) => void;
  onResetRequired: () => void;
}

interface PendingUpdate {
  data: Uint8Array;
  referenceBaseSequence: number;
  assetIDs: string[];
  introducedAssetIDs: string[];
  baseAssetsByBlock?: Map<string, string>;
  batchedUpdates?: Uint8Array[];
}

export class BoardProvider {
  readonly awareness: Awareness;
  readonly clientID = createID('cli');
  private socket: WebSocket | null = null;
  private stopped = false;
  private synced = false;
  private lifecycleVersion = 0;
  private reconnectAttempt = 0;
  private reconnectTimer = 0;
  private documentUpdateIdleTimer = 0;
  private documentUpdateMaxTimer = 0;
  private batchedUpdateID = '';
  private closeProbeAbort: AbortController | null = null;
  private referenceTimer = 0;
  private referencesInFlight = false;
  private referenceRetryAttempt = 0;
  private checkpointResponseInFlight = '';
  private checkpointPermissionDowngraded = false;
  private sequenceFrontier = 0;
  private latestKnownSequence = 0;
  private seenSequences = new Set<number>();
  private pending = new Map<string, PendingUpdate>();
  private lastAssetsByBlock: Map<string, string>;
  private remoteAwarenessIDs = new Map<string, Set<number>>();
  private trustedAwarenessUsers = new Map<number, { clientID: string; userID: string; name: string }>();
  private authoritativeCanEdit: boolean;
  private authoritativeCanManage: boolean;

  constructor(
    private readonly boardID: string,
    private readonly doc: Y.Doc,
    canEdit: boolean,
    canManage: boolean,
    private readonly callbacks: ProviderCallbacks
  ) {
    this.authoritativeCanEdit = canEdit;
    this.authoritativeCanManage = canManage;
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
    this.flushDocumentUpdates();
    this.stopped = true;
    this.lifecycleVersion += 1;
    this.cancelCloseProbe();
    window.clearTimeout(this.reconnectTimer);
    this.clearDocumentUpdateTimers();
    window.clearTimeout(this.referenceTimer);
    this.clearRemoteAwareness();
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

  flushDocumentUpdates() {
    const updateID = this.sealBatchedUpdate();
    if (!updateID || !this.synced) return;
    const pending = this.pending.get(updateID);
    if (pending) this.sendUpdate(updateID, pending);
  }

  private connect() {
    if (this.stopped) return;
    this.cancelCloseProbe();
    const lifecycleVersion = ++this.lifecycleVersion;
    this.callbacks.onConnection(this.reconnectAttempt ? 'offline' : 'connecting');
    const socket = new WebSocket(wsURL(this.boardID, this.clientID));
    this.socket = socket;
    this.synced = false;
    socket.onopen = () => {
      if (this.stopped || this.socket !== socket) return;
      this.callbacks.onConnection('syncing');
    };
    socket.onmessage = (event) => {
      if (!this.stopped && this.socket === socket) this.receive(event.data);
    };
    socket.onerror = () => {
      if (this.socket === socket) socket.close();
    };
    socket.onclose = (event) => {
      if (this.socket !== socket) return;
      this.socket = null;
      this.synced = false;
      this.checkpointResponseInFlight = '';
      this.checkpointPermissionDowngraded = false;
      window.clearTimeout(this.referenceTimer);
      if (this.stopped) return;
      if (event.reason === 'board_deleted' || event.reason === 'forbidden') {
        this.terminate(event.reason === 'board_deleted'
          ? 'This board was deleted or is no longer available'
          : 'Your access to this board was revoked');
        return;
      }
      this.clearRemoteAwareness();
      this.callbacks.onConnection('offline');
      void this.probeBoardAfterClose(lifecycleVersion);
    };
  }

  private async probeBoardAfterClose(lifecycleVersion: number) {
    const controller = new AbortController();
    this.closeProbeAbort = controller;
    const timeout = window.setTimeout(() => controller.abort(), CLOSE_PROBE_TIMEOUT_MS);
    try {
      await api(`/api/boards/${encodeURIComponent(this.boardID)}`, { signal: controller.signal });
    } catch (error) {
      if (!this.closeProbeIsCurrent(lifecycleVersion)) return;
      if (error instanceof APIError && [401, 403, 404].includes(error.status)) {
        this.terminate(error.status === 404
          ? 'This board was deleted or is no longer available'
          : 'Your access to this board was revoked');
        return;
      }
      this.scheduleReconnect();
      return;
    } finally {
      window.clearTimeout(timeout);
      if (this.closeProbeAbort === controller) this.closeProbeAbort = null;
    }
    if (this.closeProbeIsCurrent(lifecycleVersion)) this.scheduleReconnect();
  }

  private closeProbeIsCurrent(lifecycleVersion: number) {
    return !this.stopped && this.lifecycleVersion === lifecycleVersion && this.socket === null;
  }

  private cancelCloseProbe() {
    this.closeProbeAbort?.abort();
    this.closeProbeAbort = null;
  }

  private receive(raw: unknown) {
    if (this.stopped || typeof raw !== 'string') return;
    let message: WireMessage;
    try {
      const parsed = JSON.parse(raw) as unknown;
      if (!parsed || typeof parsed !== 'object') return;
      message = parsed as WireMessage;
    } catch { return; }
    if (typeof message.type !== 'string') return;
    switch (message.type) {
      case 'sync_start':
        if (message.protocol !== COLLABORATION_PROTOCOL_VERSION) {
          this.terminate(
            `Unsupported collaboration protocol ${message.protocol ?? 'unknown'}; expected v${COLLABORATION_PROTOCOL_VERSION}`,
            1002,
            'unsupported protocol'
          );
          return;
        }
        this.setAuthoritativePermission(message.can_edit === true, message.can_manage === true);
        if (!this.authoritativeCanEdit && this.pending.size > 0) {
          this.discardPendingAndReset();
          return;
        }
        this.callbacks.onConnection('syncing', message.server_sequence);
        break;
      case 'permission':
        const checkpointWasInFlight = this.checkpointResponseInFlight !== '';
        this.setAuthoritativePermission(message.can_edit === true, message.can_manage === true);
        if (checkpointWasInFlight && !this.authoritativeCanManage) this.checkpointPermissionDowngraded = true;
        if (!this.authoritativeCanEdit && this.pending.size > 0) {
          this.discardPendingAndReset();
          return;
        }
        this.scheduleReferenceSync();
        break;
      case 'checkpoint':
      case 'update':
        const applyError = message.data ? this.applyServerUpdate(message.data) : undefined;
        if (applyError && message.type === 'checkpoint') {
          this.terminate(`Stored board checkpoint is invalid: ${applyError}`, 1003, 'invalid checkpoint');
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
        this.reconnectAttempt = 0;
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
        if (this.authoritativeCanEdit && this.authoritativeCanManage && message.request_id && message.through_sequence !== undefined) {
          // A checkpoint must never be the first network message containing a
          // locally batched document change. WebSocket frames are ordered, so
          // flush the update before encoding and returning the checkpoint.
          this.flushDocumentUpdates();
          this.checkpointResponseInFlight = message.request_id;
          this.checkpointPermissionDowngraded = false;
          if (!this.send({
            type: 'checkpoint', request_id: message.request_id, through_sequence: message.through_sequence,
            data: toBase64(Y.encodeStateAsUpdate(this.doc))
          })) this.checkpointResponseInFlight = '';
        }
        break;
      case 'checkpoint_ack':
        if (message.request_id && message.request_id === this.checkpointResponseInFlight) {
          this.checkpointResponseInFlight = '';
          this.checkpointPermissionDowngraded = false;
        }
        break;
      case 'error':
        const collaborationError = message.message ?? message.code ?? 'Collaboration error';
        if (
          message.request_id &&
          ['forbidden', 'invalid_checkpoint', 'checkpoint_too_large', 'checkpoint_conflict', 'persistence_failed'].includes(message.code ?? '')
        ) {
          const matchesCurrentRequest = message.request_id === this.checkpointResponseInFlight;
          const rejectedAfterPermissionDowngrade = matchesCurrentRequest && this.checkpointPermissionDowngraded && !this.authoritativeCanManage;
          if (matchesCurrentRequest) {
            this.checkpointResponseInFlight = '';
            this.checkpointPermissionDowngraded = false;
          }
          // Permission refresh is delivered before a checkpoint rejection. A
          // manager downgrade must not also revoke the remaining board access.
          if (rejectedAfterPermissionDowngrade && message.code === 'forbidden') return;
          this.callbacks.onError(collaborationError);
          return;
        }
        if (message.code === 'forbidden') {
          if (!message.update_id) {
            this.terminate(collaborationError, 1000, 'forbidden');
            return;
          }
          this.callbacks.onError(collaborationError);
          this.setAuthoritativePermission(false, false);
          this.discardPendingAndReset();
          return;
        }
        this.callbacks.onError(collaborationError);
        if (message.update_id && ['invalid_update', 'invalid_asset_reference', 'asset_claims_required', 'update_id_conflict'].includes(message.code ?? '')) {
          this.discardPendingAndReset();
        }
        break;
    }
  }

  private receiveAwareness(message: WireMessage) {
    if (message.removed && message.client_id) {
      const ids = Array.from(this.remoteAwarenessIDs.get(message.client_id) ?? []);
      for (const id of ids) {
        if (this.trustedAwarenessUsers.get(id)?.clientID === message.client_id) this.trustedAwarenessUsers.delete(id);
      }
      if (ids.length) removeAwarenessStates(this.awareness, ids, this);
      for (const id of ids) this.awareness.meta.delete(id);
      this.remoteAwarenessIDs.delete(message.client_id);
      this.publishPresence();
      return;
    }
    if (
      !message.data || !message.client_id || !message.user_id || typeof message.user_name !== 'string' ||
      !Array.isArray(message.awareness_ids)
    ) return;
    const awarenessIDs = Array.from(new Set(message.awareness_ids));
    if (awarenessIDs.some((id) => !Number.isSafeInteger(id) || id < 0 || id === this.awareness.clientID)) return;
    for (const id of awarenessIDs) {
      const identity = this.trustedAwarenessUsers.get(id);
      const owner = this.remoteAwarenessOwner(id) ?? identity?.clientID;
      if (owner && owner !== message.client_id) {
        this.callbacks.onError('Ignored an awareness identity owned by another connection');
        return;
      }
    }
    const known = this.remoteAwarenessIDs.get(message.client_id) ?? new Set<number>();
    const hadKnown = this.remoteAwarenessIDs.has(message.client_id);
    const previousKnown = new Set(known);
    const previousIdentities = new Map(awarenessIDs.map((id) => [id, this.trustedAwarenessUsers.get(id)]));
    for (const id of awarenessIDs) {
      known.add(id);
      this.trustedAwarenessUsers.set(id, {
        clientID: message.client_id,
        userID: message.user_id,
        name: message.user_name || 'Collaborator'
      });
    }
    this.remoteAwarenessIDs.set(message.client_id, known);
    try {
      applyAwarenessUpdate(this.awareness, fromBase64(message.data), this);
    } catch {
      for (const [id, identity] of previousIdentities) {
        if (identity) this.trustedAwarenessUsers.set(id, identity);
        else this.trustedAwarenessUsers.delete(id);
      }
      if (hadKnown) this.remoteAwarenessIDs.set(message.client_id, previousKnown);
      else this.remoteAwarenessIDs.delete(message.client_id);
      this.callbacks.onError('Ignored a malformed awareness update');
      this.publishPresence();
      return;
    }
    for (const id of Array.from(known)) {
      if (!this.awareness.getStates().has(id)) {
        if (this.trustedAwarenessUsers.get(id)?.clientID === message.client_id) this.trustedAwarenessUsers.delete(id);
      }
    }
    this.publishPresence();
  }

  private remoteAwarenessOwner(awarenessID: number) {
    for (const [clientID, owned] of this.remoteAwarenessIDs) {
      if (owned.has(awarenessID)) return clientID;
    }
    return undefined;
  }

  private clearRemoteAwareness() {
    const ids = Array.from(new Set(Array.from(this.remoteAwarenessIDs.values()).flatMap((owned) => Array.from(owned))));
    this.remoteAwarenessIDs.clear();
    this.trustedAwarenessUsers.clear();
    if (ids.length) removeAwarenessStates(this.awareness, ids, this);
    for (const id of ids) this.awareness.meta.delete(id);
    this.publishPresence();
  }

  private handleDocumentUpdate = (update: Uint8Array, origin: unknown) => {
    const previousAssetsByBlock = this.lastAssetsByBlock;
    const assetsByBlock = referencedAssetsByBlock(this.doc);
    this.lastAssetsByBlock = assetsByBlock;
    if (origin === this || !this.authoritativeCanEdit) return;
    if (isBatchedBoardCommandOrigin(origin)) {
      this.mergeBatchedUpdate(update, previousAssetsByBlock, assetsByBlock);
      return;
    }
    this.flushDocumentUpdates();
    const assetIDs = Array.from(new Set(assetsByBlock.values())).sort();
    const updateID = createID('upd');
    const pending: PendingUpdate = {
      data: update.slice(),
      referenceBaseSequence: this.sequenceFrontier,
      assetIDs,
      introducedAssetIDs: introducedAssetIDs(previousAssetsByBlock, assetsByBlock)
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
      const identity = this.trustedAwarenessUsers.get(awarenessID);
      if (!identity) return;
      const state = raw as { user?: { id?: unknown; name?: unknown; color?: unknown }; cursor?: unknown; viewport?: unknown; selection?: unknown };
      presence[awarenessID] = {
        awarenessID,
        userID: identity.userID,
        name: identity.name,
        color: typeof state.user?.color === 'string' ? state.user.color : '#2563eb',
        cursor: point(state.cursor),
        viewport: viewport(state.viewport),
        selection: Array.isArray(state.selection) ? state.selection.filter((id): id is string => typeof id === 'string') : []
      };
    });
    this.callbacks.onPresence(presence);
  };

  private sendPending() {
    this.sealBatchedUpdate();
    for (const [id, update] of this.pending) this.sendUpdate(id, update);
  }

  private mergeBatchedUpdate(
    update: Uint8Array,
    previousAssetsByBlock: Map<string, string>,
    assetsByBlock: Map<string, string>
  ) {
    let updateID = this.batchedUpdateID;
    let pending = updateID ? this.pending.get(updateID) : undefined;
    if (!pending) {
      updateID = createID('upd');
      const firstUpdate = update.slice();
      pending = {
        data: firstUpdate,
        referenceBaseSequence: this.sequenceFrontier,
        assetIDs: Array.from(new Set(assetsByBlock.values())).sort(),
        introducedAssetIDs: introducedAssetIDs(previousAssetsByBlock, assetsByBlock),
        baseAssetsByBlock: new Map(previousAssetsByBlock),
        batchedUpdates: [firstUpdate]
      };
      this.batchedUpdateID = updateID;
      this.pending.set(updateID, pending);
      this.callbacks.onPending(this.pending.size);
      this.documentUpdateMaxTimer = window.setTimeout(() => this.flushDocumentUpdates(), DOCUMENT_UPDATE_MAX_MS);
    } else {
      pending.batchedUpdates?.push(update.slice());
      pending.assetIDs = Array.from(new Set(assetsByBlock.values())).sort();
      pending.introducedAssetIDs = introducedAssetIDs(pending.baseAssetsByBlock ?? previousAssetsByBlock, assetsByBlock);
    }
    window.clearTimeout(this.documentUpdateIdleTimer);
    this.documentUpdateIdleTimer = window.setTimeout(() => this.flushDocumentUpdates(), DOCUMENT_UPDATE_IDLE_MS);
  }

  private sealBatchedUpdate() {
    const updateID = this.batchedUpdateID;
    if (!updateID) return '';
    this.batchedUpdateID = '';
    this.clearDocumentUpdateTimers();
    const pending = this.pending.get(updateID);
    if (pending) {
      const updates = pending.batchedUpdates;
      if (updates?.length) pending.data = updates.length === 1 ? updates[0] : Y.mergeUpdates(updates);
      delete pending.baseAssetsByBlock;
      delete pending.batchedUpdates;
    }
    return updateID;
  }

  private clearDocumentUpdateTimers() {
    window.clearTimeout(this.documentUpdateIdleTimer);
    window.clearTimeout(this.documentUpdateMaxTimer);
    this.documentUpdateIdleTimer = 0;
    this.documentUpdateMaxTimer = 0;
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

  private setAuthoritativePermission(canEdit: boolean, canManage: boolean) {
    if (this.authoritativeCanEdit === canEdit && this.authoritativeCanManage === canManage) return;
    this.authoritativeCanEdit = canEdit;
    this.authoritativeCanManage = canManage;
    if (!canManage) window.clearTimeout(this.referenceTimer);
    this.callbacks.onPermission(canEdit, canManage);
  }

  private terminate(error: string, closeCode?: number, closeReason = '') {
    this.stopped = true;
    this.lifecycleVersion += 1;
    this.cancelCloseProbe();
    this.synced = false;
    window.clearTimeout(this.reconnectTimer);
    window.clearTimeout(this.referenceTimer);
    this.clearDocumentUpdateTimers();
    this.batchedUpdateID = '';
    this.checkpointResponseInFlight = '';
    this.checkpointPermissionDowngraded = false;
    this.pending.clear();
    this.callbacks.onPending(0);
    this.clearRemoteAwareness();
    this.setAuthoritativePermission(false, false);
    this.callbacks.onError(error);
    this.callbacks.onConnection('offline');
    const socket = this.socket;
    this.socket = null;
    if (socket && closeCode !== undefined) socket.close(closeCode, closeReason);
  }

  private discardPendingAndReset() {
    this.stopped = true;
    this.lifecycleVersion += 1;
    this.cancelCloseProbe();
    this.synced = false;
    window.clearTimeout(this.reconnectTimer);
    window.clearTimeout(this.referenceTimer);
    this.clearDocumentUpdateTimers();
    this.batchedUpdateID = '';
    this.checkpointResponseInFlight = '';
    this.checkpointPermissionDowngraded = false;
    this.pending.clear();
    this.callbacks.onPending(0);
    this.clearRemoteAwareness();
    const socket = this.socket;
    this.socket = null;
    socket?.close(1000, 'authoritative reset required');
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
      this.stopped || !this.synced || !this.authoritativeCanEdit || !this.authoritativeCanManage || this.pending.size > 0 ||
      this.sequenceFrontier !== this.latestKnownSequence
    ) return;
    window.clearTimeout(this.referenceTimer);
    this.referenceTimer = window.setTimeout(() => void this.syncReferences(), delay);
  }

  private async syncReferences() {
    if (
      this.referencesInFlight || this.stopped || !this.synced || !this.authoritativeCanEdit || !this.authoritativeCanManage || this.pending.size > 0 ||
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
      if (this.stopped || !this.authoritativeCanManage) return;
      this.referenceRetryAttempt = 0;
      this.callbacks.onError('');
    } catch (error) {
      if (!this.stopped) {
        if (error instanceof APIError && error.status === 403) {
          this.setAuthoritativePermission(this.authoritativeCanEdit, false);
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
    if (this.socket?.readyState !== WebSocket.OPEN) return false;
    this.socket.send(JSON.stringify(message));
    return true;
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
