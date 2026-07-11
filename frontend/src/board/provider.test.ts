import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as Y from 'yjs';
import { BoardProvider } from './provider';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];
  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  constructor(url: string | URL) {
    this.url = String(url);
    FakeWebSocket.instances.push(this);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event('open'));
  }

  receive(message: Record<string, unknown>) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(message) }));
  }

  send(value: string) { this.sent.push(value); }

  close() {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }
}

describe('BoardProvider', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    })));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('keeps an unacknowledged update ID and resends it after sync on reconnect', async () => {
    const doc = new Y.Doc();
    const pending: number[] = [];
    const provider = createProvider(doc, true, (count) => pending.push(count));
    provider.start();
    const first = FakeWebSocket.instances[0];
    first.open();
    first.receive({ type: 'sync_start', can_edit: true });
    first.receive({ type: 'sync_complete', server_sequence: 0 });

    doc.getMap('blocks').set('local', 'value');
    const firstUpdate = sent(first, 'update')[0];
    expect(firstUpdate.update_id).toMatch(/^upd_/);
    expect(pending[pending.length - 1]).toBe(1);

    first.close();
    await vi.advanceTimersByTimeAsync(1_000);
    const second = FakeWebSocket.instances[1];
    second.open();
    second.receive({ type: 'sync_start', can_edit: true });
    second.receive({ type: 'sync_complete', server_sequence: 0 });
    const resent = sent(second, 'update')[0];
    expect(resent.update_id).toBe(firstUpdate.update_id);
    expect(resent.data).toBe(firstUpdate.data);
    expect(resent.reference_base_sequence).toBe(0);
    expect(resent.asset_ids).toEqual([]);

    second.receive({ type: 'update_ack', update_id: resent.update_id, server_sequence: 1, duplicate: true });
    expect(pending[pending.length - 1]).toBe(0);
    provider.stop();
  });

  it('applies server updates without echoing them back and blocks viewer writes', () => {
    const doc = new Y.Doc();
    const provider = createProvider(doc, false, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: false });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    const remote = new Y.Doc();
    remote.getMap('blocks').set('remote', 'persisted');
    socket.receive({ type: 'update', update_id: 'remote-1', server_sequence: 1, data: base64(Y.encodeStateAsUpdate(remote)) });
    expect(doc.getMap('blocks').get('remote')).toBe('persisted');
    expect(sent(socket, 'update')).toHaveLength(0);

    doc.getMap('blocks').set('viewer-local', 'forbidden');
    expect(sent(socket, 'update')).toHaveLength(0);
    provider.stop();
  });

  it('drops rejected local updates, switches to read-only, and requests a clean resync', () => {
    const doc = new Y.Doc();
    const permissions: boolean[] = [];
    let resets = 0;
    const pending: number[] = [];
    const provider = createProvider(doc, true, (count) => pending.push(count), {
      onPermission: (allowed) => permissions.push(allowed),
      onResetRequired: () => { resets += 1; }
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    doc.getMap('blocks').set('local', 'rejected');
    const update = sent(socket, 'update')[0];
    socket.receive({ type: 'error', code: 'forbidden', update_id: update.update_id, message: 'viewer cannot update the document' });

    expect(permissions).toEqual([false]);
    expect(pending[pending.length - 1]).toBe(0);
    expect(resets).toBe(1);
    provider.stop();
  });

  it('publishes an exact asset reference manifest after the document is synchronized', async () => {
    const doc = new Y.Doc();
    const image = new Y.Map<unknown>();
    image.set('asset_id', 'ast_1');
    const block = new Y.Map<unknown>();
    block.set('type', 'image');
    block.set('image', image);
    doc.getMap('blocks').set('image-block', block);
    const provider = createProvider(doc, true, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'checkpoint', server_sequence: 7, data: base64(Y.encodeStateAsUpdate(doc)) });
    socket.receive({ type: 'sync_complete', server_sequence: 7 });

    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledWith('/api/boards/board-1/asset-references', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ through_sequence: 7, asset_ids: ['ast_1'] })
    }));
    provider.stop();
  });

  it('does not publish a manifest past a missing server sequence', async () => {
    const doc = new Y.Doc();
    const provider = createProvider(doc, true, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    await vi.advanceTimersByTimeAsync(100);
    vi.mocked(fetch).mockClear();

    doc.getMap('blocks').set('local', 'value');
    const local = sent(socket, 'update')[0];
    socket.receive({ type: 'update_ack', update_id: local.update_id, server_sequence: 2 });
    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).not.toHaveBeenCalled();

    const remote = new Y.Doc();
    remote.getMap('blocks').set('remote', 'value');
    socket.receive({ type: 'update', update_id: 'remote-1', server_sequence: 1, data: base64(Y.encodeStateAsUpdate(remote)) });
    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledWith('/api/boards/board-1/asset-references', expect.objectContaining({
      body: JSON.stringify({ through_sequence: 2, asset_ids: [] })
    }));
    provider.stop();
  });

  it('rebuilds the local Yjs document when a restored server has an earlier sequence', async () => {
    const doc = new Y.Doc();
    let resets = 0;
    const provider = createProvider(doc, true, () => undefined, { onResetRequired: () => { resets += 1; } });
    provider.start();
    const first = FakeWebSocket.instances[0];
    first.open();
    first.receive({ type: 'sync_start', can_edit: true });
    first.receive({ type: 'sync_complete', server_sequence: 0 });
    doc.getMap('blocks').set('local', 'value');
    const update = sent(first, 'update')[0];
    first.receive({ type: 'update_ack', update_id: update.update_id, server_sequence: 1 });

    first.close();
    await vi.advanceTimersByTimeAsync(1_000);
    const second = FakeWebSocket.instances[1];
    second.open();
    second.receive({ type: 'sync_start', can_edit: true });
    second.receive({ type: 'sync_complete', server_sequence: 0 });
    expect(resets).toBe(1);
    provider.stop();
  });

  it('retries a transient asset-reference synchronization failure', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'service_unavailable', message: 'try again' } }), {
        status: 503,
        headers: { 'Content-Type': 'application/json' }
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      }));
    const provider = createProvider(new Y.Doc(), true, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(600);
    expect(fetch).toHaveBeenCalledTimes(2);
    provider.stop();
  });

  it('does not publish authoritative asset references for a non-manager editor', async () => {
    const provider = createProvider(new Y.Doc(), true, () => undefined, {}, false);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).not.toHaveBeenCalled();
    provider.stop();
  });

  it('quarantines a malformed opaque update without breaking ordered sync', () => {
    const errors: string[] = [];
    const doc = new Y.Doc();
    const provider = createProvider(doc, true, () => undefined, { onError: (message) => errors.push(message) });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', can_edit: true });
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    socket.receive({ type: 'update', update_id: 'malformed', server_sequence: 1, data: base64(new Uint8Array([1])) });

    expect(errors[errors.length - 1]).toContain('Ignored invalid document update at sequence 1');
    expect(doc.getMap('blocks').size).toBe(0);
    provider.stop();
  });
});

function createProvider(
  doc: Y.Doc,
  canEdit: boolean,
  onPending: (count: number) => void,
  overrides: Partial<ConstructorParameters<typeof BoardProvider>[4]> = {},
  canPublishAssetReferences = true
) {
  return new BoardProvider('board-1', doc, canEdit, canPublishAssetReferences, {
    onConnection: () => undefined,
    onPending,
    onError: () => undefined,
    onPresence: () => undefined,
    onPermission: () => undefined,
    onResetRequired: () => undefined,
    ...overrides
  });
}

function sent(socket: FakeWebSocket, type: string) {
  return socket.sent.map((value) => JSON.parse(value) as Record<string, unknown>).filter((message) => message.type === type);
}

function base64(bytes: Uint8Array) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
