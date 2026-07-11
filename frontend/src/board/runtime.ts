import { useEffect, useMemo } from 'react';
import * as Y from 'yjs';
import type { User } from '../lib/api';
import { BoardCommands } from './commands';
import { BoardProvider } from './provider';
import { blocksMap, readBlocks } from './schema';
import { useBoardStore } from './store';

export interface BoardRuntime {
  doc: Y.Doc;
  commands: BoardCommands;
  provider: BoardProvider;
  canEdit: boolean;
}

export function useBoardRuntime(boardID: string, user: User, canEdit: boolean) {
  const runtime = useMemo<BoardRuntime>(() => {
    const doc = new Y.Doc();
    const editable = () => canEdit;
    const commands = new BoardCommands(doc, editable);
    const store = useBoardStore.getState;
    const provider = new BoardProvider(boardID, doc, canEdit, {
      onConnection: (state, sequence) => store().setConnection(state, sequence),
      onPending: (pending) => store().setPending(pending),
      onError: (error) => store().setConnectionError(error),
      onPresence: (presence) => store().setPresence(presence)
    });
    return { doc, commands, provider, canEdit };
  }, [boardID, canEdit]);

  useEffect(() => {
    const store = useBoardStore.getState();
    store.reset(boardID);
    const map = blocksMap(runtime.doc);
    const publish = () => useBoardStore.getState().setBlocks(readBlocks(runtime.doc));
    map.observeDeep(publish);
    publish();
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

  return runtime;
}

function collaboratorColor(seed: string) {
  const colors = ['#c2410c', '#047857', '#1d4ed8', '#7c3aed', '#be123c', '#0f766e', '#a16207', '#4338ca'];
  let hash = 0;
  for (let index = 0; index < seed.length; index += 1) hash = (hash * 31 + seed.charCodeAt(index)) >>> 0;
  return colors[hash % colors.length];
}
