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

export function useBoardRuntime(boardID: string, projectID: string, user: User, canEdit: boolean, canManage: boolean) {
  const permission = useRef(canEdit);
  const restPermission = useRef(canEdit);
  const [authoritativeCanEdit, setAuthoritativeCanEdit] = useState(canEdit);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    if (restPermission.current === canEdit) return;
    restPermission.current = canEdit;
    permission.current = canEdit;
    setAuthoritativeCanEdit(canEdit);
    setGeneration((current) => current + 1);
  }, [canEdit]);

  const runtime = useMemo<Omit<BoardRuntime, 'canEdit'>>(() => {
    const doc = new Y.Doc();
    const editable = () => permission.current;
    const commands = new BoardCommands(doc, editable);
    const store = useBoardStore.getState;
    const provider = new BoardProvider(boardID, doc, permission.current, canManage, {
      onConnection: (state, sequence) => store().setConnection(state, sequence),
      onPending: (pending) => store().setPending(pending),
      onError: (error) => store().setConnectionError(error),
      onPresence: (presence) => store().setPresence(presence),
      onPermission: (allowed) => {
        permission.current = allowed;
        setAuthoritativeCanEdit(allowed);
      },
      onResetRequired: () => setGeneration((current) => current + 1)
    });
    return { doc, commands, provider, projectID, canManage };
  }, [boardID, canManage, generation, projectID]);

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

  return useMemo(() => ({ ...runtime, canEdit: authoritativeCanEdit }), [authoritativeCanEdit, runtime]);
}

function collaboratorColor(seed: string) {
  const colors = ['#c2410c', '#047857', '#1d4ed8', '#7c3aed', '#be123c', '#0f766e', '#a16207', '#4338ca'];
  let hash = 0;
  for (let index = 0; index < seed.length; index += 1) hash = (hash * 31 + seed.charCodeAt(index)) >>> 0;
  return colors[hash % colors.length];
}
