import { useEffect, useMemo, useRef, useState } from 'react';
import * as Y from 'yjs';
import type { User } from '../lib/api';
import { BoardCommands } from './commands';
import { BoardProvider } from './provider';
import { blocksMap, readBlocks, yMapToBlock } from './schema';
import { useBoardStore } from './store';

export interface BoardRuntime {
  doc: Y.Doc;
  commands: BoardCommands;
  provider: BoardProvider;
  projectID: string;
  canEdit: boolean;
  canManage: boolean;
}

interface RuntimePermission {
  canEdit: boolean;
  canManage: boolean;
}

interface ScopedRuntimePermission extends RuntimePermission {
  scope: string;
}

export function useBoardRuntime(boardID: string, projectID: string, user: User, canEdit: boolean, canManage: boolean) {
  const scope = `${boardID}\u0000${projectID}`;
  const scopeRef = useRef(scope);
  const permission = useRef<RuntimePermission>({ canEdit, canManage });
  const restPermission = useRef<ScopedRuntimePermission>({ scope, canEdit, canManage });
  const [authoritativePermission, setAuthoritativePermission] = useState<ScopedRuntimePermission>({ scope, canEdit, canManage });
  const [generation, setGeneration] = useState(0);

  if (scopeRef.current !== scope) {
    scopeRef.current = scope;
    permission.current = { canEdit, canManage };
    restPermission.current = { scope, canEdit, canManage };
  }

  useEffect(() => {
    if (scopeRef.current !== scope) return;
    if (
      restPermission.current.scope === scope &&
      restPermission.current.canEdit === canEdit && restPermission.current.canManage === canManage
    ) {
      setAuthoritativePermission((current) => current.scope === scope ? current : { scope, canEdit, canManage });
      return;
    }
    const next = { canEdit, canManage };
    restPermission.current = { scope, ...next };
    permission.current = next;
    setAuthoritativePermission({ scope, ...next });
    setGeneration((current) => current + 1);
  }, [canEdit, canManage, scope]);

  const runtime = useMemo<Omit<BoardRuntime, 'canEdit' | 'canManage'>>(() => {
    const runtimeScope = scope;
    const doc = new Y.Doc();
    const active = () => scopeRef.current === runtimeScope;
    const editable = () => active() && permission.current.canEdit;
    const commands = new BoardCommands(doc, editable);
    const store = useBoardStore.getState;
    const provider = new BoardProvider(boardID, doc, permission.current.canEdit, permission.current.canManage, {
      onConnection: (state, sequence) => { if (active()) store().setConnection(state, sequence); },
      onPending: (pending) => { if (active()) store().setPending(pending); },
      onError: (error) => { if (active()) store().setConnectionError(error); },
      onPresence: (presence) => { if (active()) store().setPresence(presence); },
      onPermission: (allowed, manageable) => {
        if (!active()) return;
        const next = { canEdit: allowed, canManage: manageable };
        permission.current = next;
        setAuthoritativePermission({ scope: runtimeScope, ...next });
      },
      onResetRequired: () => { if (active()) setGeneration((current) => current + 1); }
    });
    return { doc, commands, provider, projectID };
  }, [boardID, generation, projectID, scope, user.email, user.id, user.name]);

  useEffect(() => {
    const store = useBoardStore.getState();
    store.reset(boardID);
    const map = blocksMap(runtime.doc);
    const publish = (events: Y.YEvent<Y.AbstractType<unknown>>[]) => {
      const ids = new Set<string>();
      for (const event of events) {
        if (event.target === map && event instanceof Y.YMapEvent) {
          for (const id of event.keysChanged) ids.add(String(id));
          continue;
        }
        if (typeof event.path[0] === 'string') ids.add(event.path[0]);
      }
      if (ids.size === 0) {
        useBoardStore.getState().setBlocks(readBlocks(runtime.doc));
        return;
      }
      useBoardStore.getState().applyBlockChanges(Array.from(ids, (id) => {
        const value = map.get(id);
        return { id, block: value instanceof Y.Map ? yMapToBlock(id, value) : null };
      }));
    };
    map.observeDeep(publish);
    useBoardStore.getState().setBlocks(readBlocks(runtime.doc));
    const viewport = useBoardStore.getState().viewport;
    runtime.provider.setLocalPresence({
      user: { id: user.id, name: user.name || user.email, color: collaboratorColor(user.id) },
      viewport,
      selection: []
    });
    runtime.provider.start();
    return () => {
      map.unobserveDeep(publish);
      runtime.provider.stop();
      runtime.commands.destroy();
      runtime.doc.destroy();
    };
  }, [boardID, runtime, user.email, user.id, user.name]);

  const authoritativeCanEdit = authoritativePermission.scope === scope ? authoritativePermission.canEdit : canEdit;
  const authoritativeCanManage = authoritativePermission.scope === scope ? authoritativePermission.canManage : canManage;
  return useMemo(() => ({
    ...runtime,
    canEdit: authoritativeCanEdit,
    canManage: authoritativeCanManage
  }), [authoritativeCanEdit, authoritativeCanManage, runtime]);
}

function collaboratorColor(seed: string) {
  const colors = ['#c2410c', '#047857', '#1d4ed8', '#7c3aed', '#be123c', '#0f766e', '#a16207', '#4338ca'];
  let hash = 0;
  for (let index = 0; index < seed.length; index += 1) hash = (hash * 31 + seed.charCodeAt(index)) >>> 0;
  return colors[hash % colors.length];
}
