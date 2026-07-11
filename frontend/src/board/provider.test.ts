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
});

function createProvider(doc: Y.Doc, canEdit: boolean, onPending: (count: number) => void) {
  return new BoardProvider('board-1', doc, canEdit, {
    onConnection: () => undefined,
    onPending,
    onError: () => undefined,
    onPresence: () => undefined
  });
}

function sent(socket: FakeWebSocket, type: string) {
  return socket.sent.map((value) => JSON.parse(value) as Record<string, string>).filter((message) => message.type === type);
}

function base64(bytes: Uint8Array) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
