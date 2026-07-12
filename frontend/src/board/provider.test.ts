import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as Y from 'yjs';
import { Awareness, encodeAwarenessUpdate } from 'y-protocols/awareness';
import { BoardCommands } from './commands';
import { BoardProvider, COLLABORATION_PROTOCOL_VERSION } from './provider';

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

  close(code = 1000, reason = '') {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close', { code, reason }));
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
    sendSyncStart(first, true);
    first.receive({ type: 'sync_complete', server_sequence: 0 });

    doc.getMap<Y.Map<unknown>>('blocks').set('local', textBlock());
    const firstUpdate = sent(first, 'update')[0];
    expect(firstUpdate.update_id).toMatch(/^upd_/);
    expect(firstUpdate.asset_ids).toEqual([]);
    expect(firstUpdate.introduced_asset_ids).toEqual([]);
    expect(pending[pending.length - 1]).toBe(1);

    first.close();
    await vi.advanceTimersByTimeAsync(1_000);
    const second = FakeWebSocket.instances[1];
    second.open();
    sendSyncStart(second, true);
    second.receive({ type: 'sync_complete', server_sequence: 0 });
    const resent = sent(second, 'update')[0];
    expect(resent.update_id).toBe(firstUpdate.update_id);
    expect(resent.data).toBe(firstUpdate.data);
    expect(resent.reference_base_sequence).toBe(0);
    expect(resent.asset_ids).toEqual([]);
    expect(resent.introduced_asset_ids).toEqual([]);

    second.receive({ type: 'update_ack', update_id: resent.update_id, server_sequence: 1, duplicate: true });
    expect(pending[pending.length - 1]).toBe(0);
    provider.stop();
  });

  it('backs off connections that close before sync and resets the delay after a successful sync', async () => {
    const random = vi.spyOn(Math, 'random').mockReturnValue(0);
    const provider = createProvider(new Y.Doc(), true, () => undefined);
    provider.start();
    const first = FakeWebSocket.instances[0];
    first.open();
    first.close();

    await vi.advanceTimersByTimeAsync(499);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    const second = FakeWebSocket.instances[1];
    second.open();
    second.close();

    await vi.advanceTimersByTimeAsync(999);
    expect(FakeWebSocket.instances).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    const third = FakeWebSocket.instances[2];
    third.open();
    sendSyncStart(third, true);
    third.receive({ type: 'sync_complete', server_sequence: 0 });
    third.close();

    await vi.advanceTimersByTimeAsync(499);
    expect(FakeWebSocket.instances).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances).toHaveLength(4);
    provider.stop();
    random.mockRestore();
  });

  it.each([
    { status: 401, expectedError: 'access to this board was revoked' },
    { status: 403, expectedError: 'access to this board was revoked' },
    { status: 404, expectedError: 'deleted or is no longer available' }
  ])('terminates reconnects when the board probe returns $status', async ({ status, expectedError }) => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({
      error: { code: status === 404 ? 'not_found' : 'forbidden', message: 'probe rejected' }
    }), {
      status,
      headers: { 'Content-Type': 'application/json' }
    }));
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onError: (message) => errors.push(message)
    });
    provider.start();
    FakeWebSocket.instances[0].close();
    await vi.advanceTimersByTimeAsync(0);

    expect(fetch).toHaveBeenCalledWith('/api/boards/board-1', expect.objectContaining({ credentials: 'include' }));
    expect(errors[errors.length - 1]).toContain(expectedError);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
    provider.stop();
  });

  it.each(['network', 'server', 'rate-limit'])('reconnects after a transient %s board-probe failure', async (failure) => {
    const random = vi.spyOn(Math, 'random').mockReturnValue(0);
    if (failure === 'network') {
      vi.mocked(fetch).mockRejectedValueOnce(new TypeError('offline'));
    } else {
      const status = failure === 'rate-limit' ? 429 : 503;
      vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({
        error: { code: failure === 'rate-limit' ? 'rate_limited' : 'service_unavailable', message: 'try again' }
      }), {
        status,
        headers: { 'Content-Type': 'application/json' }
      }));
    }
    const provider = createProvider(new Y.Doc(), true, () => undefined);
    provider.start();
    FakeWebSocket.instances[0].close();
    await vi.advanceTimersByTimeAsync(0);

    await vi.advanceTimersByTimeAsync(499);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances).toHaveLength(2);
    provider.stop();
    random.mockRestore();
  });

  it('times out a stalled board probe and resumes reconnect backoff', async () => {
    const random = vi.spyOn(Math, 'random').mockReturnValue(0);
    vi.mocked(fetch).mockImplementationOnce((_input, init) => new Promise<Response>((_resolve, reject) => {
      const signal = init?.signal;
      const abort = () => reject(new DOMException('probe timed out', 'AbortError'));
      if (signal?.aborted) abort();
      else signal?.addEventListener('abort', abort, { once: true });
    }));
    const provider = createProvider(new Y.Doc(), true, () => undefined);
    provider.start();
    FakeWebSocket.instances[0].close();

    await vi.advanceTimersByTimeAsync(4_999);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    await vi.advanceTimersByTimeAsync(499);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances).toHaveLength(2);
    provider.stop();
    random.mockRestore();
  });

  it('ignores a delayed board-probe result after a newer socket starts', async () => {
    let resolveProbe: ((response: Response) => void) | undefined;
    vi.mocked(fetch).mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveProbe = resolve; }));
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onError: (message) => errors.push(message)
    });
    provider.start();
    FakeWebSocket.instances[0].close();
    expect(fetch).toHaveBeenCalledTimes(1);

    provider.start();
    const current = FakeWebSocket.instances[1];
    current.open();
    resolveProbe?.(new Response(JSON.stringify({ error: { code: 'forbidden', message: 'stale' } }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' }
    }));
    await vi.advanceTimersByTimeAsync(0);

    expect(current.readyState).toBe(FakeWebSocket.OPEN);
    expect(errors).toEqual([]);
    provider.stop();
  });

  it('requires the collaboration protocol v5 handshake', () => {
    const errors: string[] = [];
    const permissions: Array<{ canEdit: boolean; canManage: boolean }> = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onError: (message) => errors.push(message),
      onPermission: (allowed, manageable) => permissions.push({ canEdit: allowed, canManage: manageable })
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    socket.receive({ type: 'sync_start', protocol: 4, can_edit: true });

    expect(COLLABORATION_PROTOCOL_VERSION).toBe(5);
    expect(errors[errors.length - 1]).toContain('expected v5');
    expect(permissions).toEqual([{ canEdit: false, canManage: false }]);
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    provider.stop();
  });

  it('claims new block-to-asset mappings, including reuse and an asset change on an existing block', () => {
    const doc = new Y.Doc();
    const provider = createProvider(doc, true, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    const blocks = doc.getMap<Y.Map<unknown>>('blocks');

    blocks.set('image-1', imageBlock('ast_1'));
    expect(latestSent(socket, 'update')).toMatchObject({
      asset_ids: ['ast_1'],
      introduced_asset_ids: ['ast_1']
    });

    blocks.set('image-2', imageBlock('ast_1'));
    expect(latestSent(socket, 'update')).toMatchObject({
      asset_ids: ['ast_1'],
      introduced_asset_ids: ['ast_1']
    });

    const image = blocks.get('image-1')?.get('image');
    expect(image).toBeInstanceOf(Y.Map);
    (image as Y.Map<unknown>).set('asset_id', 'ast_2');
    expect(latestSent(socket, 'update')).toMatchObject({
      asset_ids: ['ast_1', 'ast_2'],
      introduced_asset_ids: ['ast_2']
    });
    provider.stop();
  });

  it('sends empty claims for deletion and redo, but reclaims the restored mapping on undo', () => {
    const doc = new Y.Doc();
    doc.getMap<Y.Map<unknown>>('blocks').set('image-1', imageBlock('ast_1'));
    const commands = new BoardCommands(doc, () => true);
    const provider = createProvider(doc, true, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    commands.delete(['image-1']);
    commands.stopCapturing();
    commands.undo();
    commands.redo();

    const updates = sent(socket, 'update');
    expect(updates).toHaveLength(3);
    expect(updates[0]).toMatchObject({ asset_ids: [], introduced_asset_ids: [] });
    expect(updates[1]).toMatchObject({ asset_ids: ['ast_1'], introduced_asset_ids: ['ast_1'] });
    expect(updates[2]).toMatchObject({ asset_ids: [], introduced_asset_ids: [] });
    commands.destroy();
    provider.stop();
  });

  it('applies server updates without echoing them back and blocks viewer writes', () => {
    const doc = new Y.Doc();
    const provider = createProvider(doc, false, () => undefined);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, false);
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

  it('applies live edit and manager permission changes atomically', async () => {
    const doc = new Y.Doc();
    let permission = { canEdit: true, canManage: false };
    const permissions: typeof permission[] = [];
    const commands = new BoardCommands(doc, () => permission.canEdit);
    const provider = createProvider(doc, true, () => undefined, {
      onPermission: (canEdit, canManage) => {
        permission = { canEdit, canManage };
        permissions.push(permission);
      }
    }, false);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, false);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    socket.receive({ type: 'permission', can_edit: false, can_manage: false });
    expect(permission).toEqual({ canEdit: false, canManage: false });
    expect(commands.createText(0, 0)).toBe('');

    socket.receive({ type: 'permission', can_edit: true, can_manage: false });
    const blockID = commands.createText(10, 20);
    expect(blockID).not.toBe('');
    const update = latestSent(socket, 'update');
    socket.receive({ type: 'update_ack', update_id: update.update_id, server_sequence: 1 });
    vi.mocked(fetch).mockClear();

    socket.receive({ type: 'permission', can_edit: true, can_manage: true });
    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledWith('/api/boards/board-1/asset-references', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ through_sequence: 1, asset_ids: [] })
    }));
    socket.receive({ type: 'checkpoint_request', request_id: 'checkpoint-1', through_sequence: 1 });
    expect(latestSent(socket, 'checkpoint')).toMatchObject({ request_id: 'checkpoint-1', through_sequence: 1 });
    expect(permissions).toEqual([
      { canEdit: false, canManage: false },
      { canEdit: true, canManage: false },
      { canEdit: true, canManage: true }
    ]);
    commands.destroy();
    provider.stop();
  });

  it('keeps edit access when a manager downgrade rejects an in-flight checkpoint response', () => {
    const doc = new Y.Doc();
    let permission = { canEdit: true, canManage: true };
    const permissions: typeof permission[] = [];
    let resets = 0;
    const commands = new BoardCommands(doc, () => permission.canEdit);
    const provider = createProvider(doc, true, () => undefined, {
      onPermission: (canEdit, canManage) => {
        permission = { canEdit, canManage };
        permissions.push(permission);
      },
      onResetRequired: () => { resets += 1; }
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    socket.receive({ type: 'checkpoint_request', request_id: 'checkpoint-race', through_sequence: 1 });
    expect(latestSent(socket, 'checkpoint')).toMatchObject({ request_id: 'checkpoint-race' });

    socket.receive({ type: 'permission', can_edit: true, can_manage: false });
    socket.receive({
      type: 'error', request_id: 'checkpoint-race', code: 'forbidden', message: 'only a project manager can save a checkpoint'
    });

    expect(permission).toEqual({ canEdit: true, canManage: false });
    expect(permissions).toEqual([{ canEdit: true, canManage: false }]);
    expect(resets).toBe(0);
    expect(commands.createText(0, 0)).not.toBe('');
    commands.destroy();
    provider.stop();
  });

  it('keeps viewer access when a manager-to-viewer downgrade rejects an in-flight checkpoint response', () => {
    const permissions: Array<{ canEdit: boolean; canManage: boolean }> = [];
    let resets = 0;
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onPermission: (canEdit, canManage) => permissions.push({ canEdit, canManage }),
      onResetRequired: () => { resets += 1; }
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    socket.receive({ type: 'checkpoint_request', request_id: 'checkpoint-viewer-race', through_sequence: 1 });

    socket.receive({ type: 'permission', can_edit: false, can_manage: false });
    socket.receive({
      type: 'error', request_id: 'checkpoint-viewer-race', code: 'forbidden', message: 'only a project manager can save a checkpoint'
    });

    expect(permissions).toEqual([{ canEdit: false, canManage: false }]);
    expect(resets).toBe(0);
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    provider.stop();
  });

  it('does not let an old checkpoint error clear the current request association', () => {
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onError: (message) => errors.push(message)
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    socket.receive({ type: 'checkpoint_request', request_id: 'checkpoint-old', through_sequence: 1 });
    socket.receive({ type: 'checkpoint_request', request_id: 'checkpoint-current', through_sequence: 1 });

    socket.receive({
      type: 'error', request_id: 'checkpoint-old', code: 'invalid_checkpoint', message: 'old checkpoint failed'
    });
    socket.receive({ type: 'permission', can_edit: true, can_manage: false });
    socket.receive({
      type: 'error', request_id: 'checkpoint-current', code: 'forbidden', message: 'only a project manager can save a checkpoint'
    });

    expect(errors).toEqual(['old checkpoint failed']);
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    provider.stop();
  });

  it('discards pending updates when a live permission message removes edit access', () => {
    const doc = new Y.Doc();
    const pending: number[] = [];
    let resets = 0;
    const provider = createProvider(doc, true, (count) => pending.push(count), {
      onResetRequired: () => { resets += 1; }
    }, false);
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, false);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    doc.getMap('blocks').set('pending', 'value');
    expect(pending[pending.length - 1]).toBe(1);

    socket.receive({ type: 'permission', can_edit: false, can_manage: false });
    expect(pending[pending.length - 1]).toBe(0);
    expect(resets).toBe(1);
    provider.stop();
  });

  it.each([
    ['board_deleted', 'deleted or is no longer available'],
    ['forbidden', 'access to this board was revoked']
  ])('treats the %s close reason as terminal', async (reason, expectedError) => {
    const pending: number[] = [];
    const errors: string[] = [];
    const connections: string[] = [];
    const presence: Array<Record<number, unknown>> = [];
    const doc = new Y.Doc();
    const provider = createProvider(doc, true, (count) => pending.push(count), {
      onError: (message) => errors.push(message),
      onConnection: (state) => connections.push(state),
      onPresence: (value) => presence.push(value)
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    doc.getMap('blocks').set('pending', 'value');

    const remoteDoc = new Y.Doc();
    const remote = new Awareness(remoteDoc);
    remote.setLocalState({ user: { color: '#123456' } });
    socket.receive({
      type: 'awareness', client_id: 'cli_remote', user_id: 'usr_remote', user_name: 'Remote',
      awareness_ids: [remote.clientID], data: base64(encodeAwarenessUpdate(remote, [remote.clientID]))
    });
    expect(Object.keys(presence[presence.length - 1])).toHaveLength(1);

    socket.close(1000, reason);
    expect(pending[pending.length - 1]).toBe(0);
    expect(presence[presence.length - 1]).toEqual({});
    expect(errors[errors.length - 1]).toContain(expectedError);
    expect(connections[connections.length - 1]).toBe('offline');
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
    remote.destroy();
    remoteDoc.destroy();
    provider.stop();
  });

  it('does not let an in-flight asset sync clear a terminal close error', async () => {
    let resolveFetch: ((response: Response) => void) | undefined;
    vi.mocked(fetch).mockImplementation(() => new Promise<Response>((resolve) => { resolveFetch = resolve; }));
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onError: (message) => errors.push(message)
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    await vi.advanceTimersByTimeAsync(50);
    expect(fetch).toHaveBeenCalledTimes(1);

    socket.close(1000, 'forbidden');
    expect(errors[errors.length - 1]).toContain('access to this board was revoked');
    resolveFetch?.(new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    }));
    await vi.advanceTimersByTimeAsync(0);

    expect(errors[errors.length - 1]).toContain('access to this board was revoked');
    provider.stop();
  });

  it('ignores delayed socket messages after the provider has stopped', () => {
    const permissions: Array<{ canEdit: boolean; canManage: boolean }> = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onPermission: (canEdit, canManage) => permissions.push({ canEdit, canManage })
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    provider.stop();

    socket.receive({ type: 'permission', can_edit: false, can_manage: false });
    expect(permissions).toEqual([]);
  });

  it('uses the authenticated awareness envelope instead of client-supplied identity fields', () => {
    const presence: Array<Record<number, { userID: string; name: string; color: string }>> = [];
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onPresence: (value) => presence.push(value),
      onError: (message) => errors.push(message)
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    const remoteDoc = new Y.Doc();
    const remote = new Awareness(remoteDoc);
    remote.setLocalState({
      user: { id: 'forged-user', name: 'Forged Name', color: '#123456' },
      cursor: { x: 12, y: 34 },
      selection: ['block-1']
    });
    const update = encodeAwarenessUpdate(remote, [remote.clientID]);
    socket.receive({
      type: 'awareness',
      client_id: 'cli_remote',
      user_id: 'usr_trusted',
      user_name: 'Trusted User',
      awareness_ids: [remote.clientID],
      data: base64(update)
    });

    expect(presence[presence.length - 1][remote.clientID]).toMatchObject({
      userID: 'usr_trusted',
      name: 'Trusted User',
      color: '#123456'
    });

    socket.receive({
      type: 'awareness',
      client_id: 'cli_attacker',
      user_id: 'usr_attacker',
      user_name: 'Attacker',
      awareness_ids: [remote.clientID],
      data: base64(update)
    });
    expect(errors[errors.length - 1]).toContain('owned by another connection');
    expect(presence[presence.length - 1][remote.clientID]).toMatchObject({
      userID: 'usr_trusted',
      name: 'Trusted User'
    });

    socket.receive({
      type: 'awareness',
      client_id: 'cli_broken',
      user_id: 'usr_broken',
      user_name: 'Broken',
      awareness_ids: [999],
      data: base64(new Uint8Array([1]))
    });
    expect(errors[errors.length - 1]).toContain('malformed awareness');

    const secondDoc = new Y.Doc();
    const second = new Awareness(secondDoc);
    second.states.set(999, { user: { id: 'another-forged-user', name: 'Another Forgery', color: '#654321' } });
    second.meta.set(999, { clock: 1, lastUpdated: 0 });
    socket.receive({
      type: 'awareness',
      client_id: 'cli_second',
      user_id: 'usr_second',
      user_name: 'Second Trusted User',
      awareness_ids: [999],
      data: base64(encodeAwarenessUpdate(second, [999]))
    });
    expect(presence[presence.length - 1][999]).toMatchObject({
      userID: 'usr_second',
      name: 'Second Trusted User',
      color: '#654321'
    });

    socket.close();
    expect(presence[presence.length - 1]).toEqual({});
    second.destroy();
    secondDoc.destroy();
    remote.destroy();
    remoteDoc.destroy();
    provider.stop();
  });

  it('releases tombstoned awareness ownership so a lower-clock connection can reclaim the ID', () => {
    const presence: Array<Record<number, { userID: string }>> = [];
    const errors: string[] = [];
    const provider = createProvider(new Y.Doc(), true, () => undefined, {
      onPresence: (value) => presence.push(value),
      onError: (message) => errors.push(message)
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    const remoteDoc = new Y.Doc();
    const remote = new Awareness(remoteDoc);
    remote.setLocalState({ user: { color: '#123456' } });
    const stateClock = remote.meta.get(remote.clientID)?.clock ?? 0;
    const data = base64(encodeAwarenessUpdate(remote, [remote.clientID]));
    socket.receive({
      type: 'awareness', client_id: 'cli_old', user_id: 'usr_remote', user_name: 'Remote',
      awareness_ids: [remote.clientID], data
    });
    expect(presence[presence.length - 1][remote.clientID]?.userID).toBe('usr_remote');

    remote.setLocalState(null);
    expect(remote.meta.get(remote.clientID)?.clock ?? 0).toBeGreaterThan(stateClock);
    socket.receive({
      type: 'awareness', client_id: 'cli_old', user_id: 'usr_remote', user_name: 'Remote',
      awareness_ids: [remote.clientID], data: base64(encodeAwarenessUpdate(remote, [remote.clientID]))
    });
    expect(presence[presence.length - 1]).toEqual({});

    socket.receive({
      type: 'awareness', client_id: 'cli_new', user_id: 'usr_remote', user_name: 'Remote',
      awareness_ids: [remote.clientID], data
    });
    expect(errors[errors.length - 1]).toContain('owned by another connection');
    expect(presence[presence.length - 1]).toEqual({});

    socket.receive({ type: 'awareness', client_id: 'cli_old', removed: true });
    expect(presence[presence.length - 1]).toEqual({});
    socket.receive({
      type: 'awareness', client_id: 'cli_new', user_id: 'usr_remote', user_name: 'Remote',
      awareness_ids: [remote.clientID], data
    });
    expect(presence[presence.length - 1][remote.clientID]?.userID).toBe('usr_remote');

    remote.destroy();
    remoteDoc.destroy();
    provider.stop();
  });

  it('drops rejected local updates, switches to read-only, and requests a clean resync', () => {
    const doc = new Y.Doc();
    const permissions: Array<{ canEdit: boolean; canManage: boolean }> = [];
    let resets = 0;
    const pending: number[] = [];
    const provider = createProvider(doc, true, (count) => pending.push(count), {
      onPermission: (allowed, manageable) => permissions.push({ canEdit: allowed, canManage: manageable }),
      onResetRequired: () => { resets += 1; }
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });

    doc.getMap('blocks').set('local', 'rejected');
    const update = sent(socket, 'update')[0];
    socket.receive({ type: 'error', code: 'forbidden', update_id: update.update_id, message: 'viewer cannot update the document' });

    expect(permissions).toEqual([{ canEdit: false, canManage: false }]);
    expect(pending[pending.length - 1]).toBe(0);
    expect(resets).toBe(1);
    provider.stop();
  });

  it('treats a forbidden error without an update ID as revoked board access', async () => {
    const pending: number[] = [];
    const permissions: Array<{ canEdit: boolean; canManage: boolean }> = [];
    let resets = 0;
    const doc = new Y.Doc();
    const provider = createProvider(doc, true, (count) => pending.push(count), {
      onPermission: (canEdit, canManage) => permissions.push({ canEdit, canManage }),
      onResetRequired: () => { resets += 1; }
    });
    provider.start();
    const socket = FakeWebSocket.instances[0];
    socket.open();
    sendSyncStart(socket, true, true);
    socket.receive({ type: 'sync_complete', server_sequence: 0 });
    doc.getMap('blocks').set('pending', 'value');

    socket.receive({ type: 'error', code: 'forbidden', message: 'board access was revoked' });

    expect(permissions).toEqual([{ canEdit: false, canManage: false }]);
    expect(pending[pending.length - 1]).toBe(0);
    expect(resets).toBe(0);
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
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
    sendSyncStart(socket, true);
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
    sendSyncStart(socket, true);
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
    sendSyncStart(first, true);
    first.receive({ type: 'sync_complete', server_sequence: 0 });
    doc.getMap('blocks').set('local', 'value');
    const update = sent(first, 'update')[0];
    first.receive({ type: 'update_ack', update_id: update.update_id, server_sequence: 1 });

    first.close();
    await vi.advanceTimersByTimeAsync(1_000);
    const second = FakeWebSocket.instances[1];
    second.open();
    sendSyncStart(second, true);
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
    sendSyncStart(socket, true);
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
    sendSyncStart(socket, true, false);
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
    sendSyncStart(socket, true);
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
  canManage = canEdit
) {
  return new BoardProvider('board-1', doc, canEdit, canManage, {
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

function latestSent(socket: FakeWebSocket, type: string) {
  const messages = sent(socket, type);
  const message = messages[messages.length - 1];
  if (!message) throw new Error(`Expected a sent ${type} message`);
  return message;
}

function sendSyncStart(socket: FakeWebSocket, canEdit: boolean, canManage = canEdit) {
  socket.receive({ type: 'sync_start', protocol: COLLABORATION_PROTOCOL_VERSION, can_edit: canEdit, can_manage: canManage });
}

function imageBlock(assetID: string) {
  const image = new Y.Map<unknown>();
  image.set('asset_id', assetID);
  const block = new Y.Map<unknown>();
  block.set('type', 'image');
  block.set('image', image);
  return block;
}

function textBlock() {
  const block = new Y.Map<unknown>();
  block.set('type', 'text');
  block.set('text', new Y.Text());
  return block;
}

function base64(bytes: Uint8Array) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
