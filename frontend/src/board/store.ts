import { create } from 'zustand';
import type { Rect, WhiteboardBlock } from './schema';

export type BoardTool = 'select' | 'text' | 'pan';
export type ConnectionState = 'connecting' | 'syncing' | 'live' | 'offline';

export interface Viewport {
  x: number;
  y: number;
  scale: number;
}

export interface RemotePresence {
  awarenessID: number;
  userID: string;
  name: string;
  color: string;
  cursor?: { x: number; y: number };
  viewport?: Viewport;
  selection: string[];
}

interface BoardState {
  boardID: string;
  document: { blocks: WhiteboardBlock[]; revision: number };
  selection: { ids: string[]; box: Rect | null };
  viewport: Viewport;
  tool: BoardTool;
  interaction: { kind: 'idle' | 'pan' | 'select-box' | 'move' | 'resize'; active: boolean };
  connection: { state: ConnectionState; pending: number; lastSequence: number; error: string };
  presence: Record<number, RemotePresence>;
  reset: (boardID: string) => void;
  setBlocks: (blocks: WhiteboardBlock[]) => void;
  setSelection: (ids: string[]) => void;
  toggleSelection: (id: string) => void;
  setSelectionBox: (box: Rect | null) => void;
  setViewport: (viewport: Viewport) => void;
  setTool: (tool: BoardTool) => void;
  setInteraction: (kind: BoardState['interaction']['kind'], active: boolean) => void;
  setConnection: (state: ConnectionState, lastSequence?: number) => void;
  setPending: (pending: number) => void;
  setConnectionError: (error: string) => void;
  setPresence: (presence: Record<number, RemotePresence>) => void;
}

const initialViewport: Viewport = { x: 80, y: 80, scale: 1 };

export const useBoardStore = create<BoardState>((set) => ({
  boardID: '',
  document: { blocks: [], revision: 0 },
  selection: { ids: [], box: null },
  viewport: initialViewport,
  tool: 'select',
  interaction: { kind: 'idle', active: false },
  connection: { state: 'connecting', pending: 0, lastSequence: 0, error: '' },
  presence: {},
  reset: (boardID) => set({
    boardID,
    document: { blocks: [], revision: 0 },
    selection: { ids: [], box: null },
    viewport: loadViewport(boardID),
    tool: 'select',
    interaction: { kind: 'idle', active: false },
    connection: { state: 'connecting', pending: 0, lastSequence: 0, error: '' },
    presence: {}
  }),
  setBlocks: (blocks) => set((state) => ({ document: { blocks, revision: state.document.revision + 1 } })),
  setSelection: (ids) => set({ selection: { ids: unique(ids), box: null } }),
  toggleSelection: (id) => set((state) => ({ selection: {
    ids: state.selection.ids.includes(id) ? state.selection.ids.filter((next) => next !== id) : [...state.selection.ids, id],
    box: null
  } })),
  setSelectionBox: (box) => set((state) => ({ selection: { ...state.selection, box } })),
  setViewport: (viewport) => set((state) => {
    saveViewport(state.boardID, viewport);
    return { viewport };
  }),
  setTool: (tool) => set({ tool }),
  setInteraction: (kind, active) => set({ interaction: { kind, active } }),
  setConnection: (connectionState, lastSequence) => set((state) => ({ connection: {
    ...state.connection,
    state: connectionState,
    lastSequence: lastSequence ?? state.connection.lastSequence,
    error: connectionState === 'live' ? '' : state.connection.error
  } })),
  setPending: (pending) => set((state) => ({ connection: { ...state.connection, pending } })),
  setConnectionError: (error) => set((state) => ({ connection: { ...state.connection, error } })),
  setPresence: (presence) => set({ presence })
}));

function unique(ids: string[]) {
  return Array.from(new Set(ids));
}

function loadViewport(boardID: string): Viewport {
  try {
    const value = JSON.parse(localStorage.getItem(`dw_viewport_${boardID}`) ?? '') as Viewport;
    if (Number.isFinite(value.x) && Number.isFinite(value.y) && Number.isFinite(value.scale)) return { ...value, scale: clamp(value.scale, 0.15, 4) };
  } catch { /* use default */ }
  return { ...initialViewport };
}

function saveViewport(boardID: string, viewport: Viewport) {
  if (!boardID) return;
  localStorage.setItem(`dw_viewport_${boardID}`, JSON.stringify(viewport));
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}
