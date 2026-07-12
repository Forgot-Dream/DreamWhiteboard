import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { COLLABORATION_PROTOCOL_VERSION } from './provider';
import { useBoardRuntime } from './runtime';

class RuntimeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: RuntimeWebSocket[] = [];
  readyState = RuntimeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  constructor(_url: string | URL) {
    RuntimeWebSocket.instances.push(this);
  }

  open() {
    this.readyState = RuntimeWebSocket.OPEN;
    this.onopen?.(new Event('open'));
  }

  receive(message: Record<string, unknown>) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(message) }));
  }

  send(_value: string) { /* captured by provider tests */ }

  close(code = 1000, reason = '') {
    if (this.readyState === RuntimeWebSocket.CLOSED) return;
    this.readyState = RuntimeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close', { code, reason }));
  }
}

describe('useBoardRuntime permissions', () => {
  beforeEach(() => {
    RuntimeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', RuntimeWebSocket as unknown as typeof WebSocket);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    })));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps command guards and both runtime permission flags in sync with the provider', () => {
    const user = {
      id: 'usr_1', email: 'editor@example.com', name: 'Editor', system_role: 'user' as const,
      created_at: '2026-07-12T00:00:00Z'
    };
    const view = renderHook(() => useBoardRuntime('board-1', 'project-1', user, true, true));
    const socket = RuntimeWebSocket.instances[0];

    act(() => {
      socket.open();
      socket.receive({
        type: 'sync_start', protocol: COLLABORATION_PROTOCOL_VERSION,
        can_edit: false, can_manage: false
      });
      socket.receive({ type: 'sync_complete', server_sequence: 0 });
    });
    expect(view.result.current).toMatchObject({ canEdit: false, canManage: false });
    expect(view.result.current.commands.createText(0, 0)).toBe('');

    act(() => socket.receive({ type: 'permission', can_edit: true, can_manage: false }));
    expect(view.result.current).toMatchObject({ canEdit: true, canManage: false });
    expect(view.result.current.commands.createText(10, 20)).not.toBe('');

    act(() => socket.receive({ type: 'permission', can_edit: true, can_manage: true }));
    expect(view.result.current).toMatchObject({ canEdit: true, canManage: true });
    view.unmount();
  });

  it('recreates the collaboration runtime when the local presence identity changes', () => {
    const baseUser = {
      id: 'usr_1', email: 'editor@example.com', name: 'Editor', system_role: 'user' as const,
      created_at: '2026-07-12T00:00:00Z'
    };
    const view = renderHook(
      ({ name }) => useBoardRuntime('board-1', 'project-1', { ...baseUser, name }, true, true),
      { initialProps: { name: 'Editor' } }
    );
    const firstDocument = view.result.current.doc;

    view.rerender({ name: 'Renamed Editor' });

    expect(view.result.current.doc).not.toBe(firstDocument);
    expect(RuntimeWebSocket.instances).toHaveLength(2);
    view.unmount();
  });

  it('resets dynamic permissions synchronously when the board scope changes', () => {
    const user = {
      id: 'usr_1', email: 'editor@example.com', name: 'Editor', system_role: 'user' as const,
      created_at: '2026-07-12T00:00:00Z'
    };
    const view = renderHook(
      ({ boardID, projectID }) => useBoardRuntime(boardID, projectID, user, true, true),
      { initialProps: { boardID: 'board-1', projectID: 'project-1' } }
    );
    const firstRuntime = view.result.current;
    const firstSocket = RuntimeWebSocket.instances[0];
    act(() => {
      firstSocket.open();
      firstSocket.receive({
        type: 'sync_start', protocol: COLLABORATION_PROTOCOL_VERSION,
        can_edit: false, can_manage: false
      });
    });
    expect(view.result.current).toMatchObject({ canEdit: false, canManage: false });

    view.rerender({ boardID: 'board-2', projectID: 'project-2' });

    expect(view.result.current.doc).not.toBe(firstRuntime.doc);
    expect(view.result.current).toMatchObject({ canEdit: true, canManage: true });
    expect(view.result.current.commands.createText(10, 20)).not.toBe('');
    expect(RuntimeWebSocket.instances).toHaveLength(2);

    act(() => firstSocket.receive({ type: 'permission', can_edit: false, can_manage: false }));
    expect(view.result.current).toMatchObject({ canEdit: true, canManage: true });
    view.unmount();
  });
});
