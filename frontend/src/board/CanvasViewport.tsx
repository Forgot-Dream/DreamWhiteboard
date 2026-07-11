import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from 'react';
import type { BoardRuntime } from './runtime';
import { imageSource, intersects, type ImageBlock, type Rect, type WhiteboardBlock } from './schema';
import { useBoardStore, type RemotePresence, type Viewport } from './store';

type ResizeHandle = 'n' | 'ne' | 'e' | 'se' | 's' | 'sw' | 'w' | 'nw';
type ActiveInteraction =
  | { kind: 'pan'; start: Point; origin: Viewport }
  | { kind: 'select-box'; start: Point; additive: boolean }
  | { kind: 'move'; ids: string[]; last: Point }
  | { kind: 'resize'; block: WhiteboardBlock; handle: ResizeHandle; start: Point };
type Point = { x: number; y: number };

export function CanvasViewport({ runtime }: { runtime: BoardRuntime }) {
  const canvas = useRef<HTMLDivElement>(null);
  const interaction = useRef<ActiveInteraction | null>(null);
  const latestPointer = useRef<Point | null>(null);
  const pointerFrame = useRef(0);
  const latestCursor = useRef<Point | null>(null);
  const cursorFrame = useRef(0);
  const [canvasSize, setCanvasSize] = useState({ width: window.innerWidth, height: Math.max(1, window.innerHeight - 58) });
  const blocks = useBoardStore((state) => state.document.blocks);
  const selectedIDs = useBoardStore((state) => state.selection.ids);
  const selectionBox = useBoardStore((state) => state.selection.box);
  const viewport = useBoardStore((state) => state.viewport);
  const tool = useBoardStore((state) => state.tool);
  const presence = useBoardStore((state) => state.presence);
  const remotes = useMemo(() => Object.values(presence), [presence]);
  const selectedSet = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const visible = useMemo(() => filterVisibleBlocks(blocks, viewport, canvasSize, selectedSet), [blocks, canvasSize, selectedSet, viewport]);

  useEffect(() => {
    if (!canvas.current) return;
    const observer = new ResizeObserver(([entry]) => setCanvasSize({ width: entry.contentRect.width, height: entry.contentRect.height }));
    observer.observe(canvas.current);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    runtime.provider.updateLocalPresence('selection', selectedIDs);
  }, [runtime.provider, selectedIDs]);

  useEffect(() => {
    runtime.provider.updateLocalPresence('viewport', viewport);
  }, [runtime.provider, viewport]);

  useEffect(() => () => {
    cancelAnimationFrame(pointerFrame.current);
    cancelAnimationFrame(cursorFrame.current);
  }, []);

  function pointerDownCanvas(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.button !== 0 && event.button !== 1) return;
    // Creating an auto-focused textarea during pointerdown must suppress the
    // browser's later background-focus default, otherwise the new empty block
    // immediately blurs and is removed before the user can type.
    event.preventDefault();
    const screen = localPoint(event);
    const world = toWorld(screen, viewport);
    if (event.button === 1 || tool === 'pan') {
      interaction.current = { kind: 'pan', start: screen, origin: viewport };
      useBoardStore.getState().setInteraction('pan', true);
    } else if (tool === 'text' && runtime.canEdit) {
      const id = runtime.commands.createText(world.x - 8, world.y - 8);
      if (id) useBoardStore.getState().setSelection([id]);
      return;
    } else {
      interaction.current = { kind: 'select-box', start: world, additive: event.shiftKey };
      useBoardStore.getState().setSelectionBox({ x: world.x, y: world.y, width: 0, height: 0 });
      useBoardStore.getState().setInteraction('select-box', true);
    }
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerDownBlock(event: ReactPointerEvent<HTMLElement>, block: WhiteboardBlock) {
    event.stopPropagation();
    const store = useBoardStore.getState();
    if (event.shiftKey) store.toggleSelection(block.id);
    else if (!store.selection.ids.includes(block.id)) store.setSelection([block.id]);
    if (!runtime.canEdit || event.button !== 0) return;
    const ids = useBoardStore.getState().selection.ids;
    if (!ids.includes(block.id)) return;
    interaction.current = { kind: 'move', ids, last: toWorld(localPoint(event), viewport) };
    store.setInteraction('move', true);
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerDownResize(event: ReactPointerEvent<HTMLButtonElement>, block: WhiteboardBlock, handle: ResizeHandle) {
    event.stopPropagation();
    if (!runtime.canEdit) return;
    interaction.current = { kind: 'resize', block, handle, start: toWorld(localPoint(event), viewport) };
    useBoardStore.getState().setInteraction('resize', true);
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    const screen = localPoint(event);
    latestCursor.current = toWorld(screen, useBoardStore.getState().viewport);
    if (!cursorFrame.current) cursorFrame.current = requestAnimationFrame(() => {
      cursorFrame.current = 0;
      if (latestCursor.current) runtime.provider.updateLocalPresence('cursor', latestCursor.current);
    });
    if (!interaction.current) return;
    latestPointer.current = screen;
    if (!pointerFrame.current) pointerFrame.current = requestAnimationFrame(applyLatestPointer);
  }

  function applyLatestPointer() {
    pointerFrame.current = 0;
    const active = interaction.current;
    const screen = latestPointer.current;
    if (!active || !screen) return;
    const store = useBoardStore.getState();
    if (active.kind === 'pan') {
      store.setViewport({ ...active.origin, x: active.origin.x + screen.x - active.start.x, y: active.origin.y + screen.y - active.start.y });
      return;
    }
    const world = toWorld(screen, store.viewport);
    if (active.kind === 'select-box') {
      store.setSelectionBox(normalizeRect(active.start, world));
      return;
    }
    if (active.kind === 'move') {
      runtime.commands.move(active.ids, world.x - active.last.x, world.y - active.last.y);
      active.last = world;
      return;
    }
    runtime.commands.resize(active.block.id, resizeRect(active.block, active.handle, active.start, world));
  }

  function pointerUp(event: ReactPointerEvent<HTMLDivElement>) {
    if (pointerFrame.current) {
      cancelAnimationFrame(pointerFrame.current);
      pointerFrame.current = 0;
      latestPointer.current = localPoint(event);
      applyLatestPointer();
    }
    const active = interaction.current;
    if (active?.kind === 'select-box') {
      const store = useBoardStore.getState();
      const box = store.selection.box;
      if (box) {
        const matches = store.document.blocks.filter((block) => intersects(box, blockRect(block))).map((block) => block.id);
        store.setSelection(active.additive ? [...store.selection.ids, ...matches] : matches);
      }
    }
    if (active?.kind === 'move' || active?.kind === 'resize') runtime.commands.stopCapturing();
    interaction.current = null;
    latestPointer.current = null;
    useBoardStore.getState().setSelectionBox(null);
    useBoardStore.getState().setInteraction('idle', false);
  }

  function wheel(event: ReactWheelEvent<HTMLDivElement>) {
    event.preventDefault();
    const store = useBoardStore.getState();
    const screen = localPoint(event);
    const before = toWorld(screen, store.viewport);
    const scale = clamp(store.viewport.scale * Math.exp(-event.deltaY * 0.0015), 0.15, 4);
    store.setViewport({ x: screen.x - before.x * scale, y: screen.y - before.y * scale, scale });
  }

  return (
    <div
      ref={canvas}
      className={`canvas canvas-tool-${tool}`}
      onPointerDown={pointerDownCanvas}
      onPointerMove={pointerMove}
      onPointerUp={pointerUp}
      onPointerCancel={pointerUp}
      onWheel={wheel}
    >
      <div className="world" style={{ transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.scale})` }}>
        {visible.map((block) => (
          <BlockView
            key={block.id}
            block={block}
            selected={selectedSet.has(block.id)}
            onlySelection={selectedIDs.length === 1}
            readOnly={!runtime.canEdit}
            remotes={remotes.filter((remote) => remote.selection.includes(block.id))}
            onPointerDown={(event) => pointerDownBlock(event, block)}
            onResize={(event, handle) => pointerDownResize(event, block, handle)}
            onSelect={(additive) => additive ? useBoardStore.getState().toggleSelection(block.id) : useBoardStore.getState().setSelection([block.id])}
            onText={(value) => runtime.commands.replaceText(block.id, value)}
            onEmptyBlur={() => { if (block.type === 'text' && !block.text.trim()) { runtime.commands.delete([block.id]); useBoardStore.getState().setSelection([]); } }}
          />
        ))}
        {selectionBox && <div className="selection-box" style={{ left: selectionBox.x, top: selectionBox.y, width: selectionBox.width, height: selectionBox.height }} />}
        {remotes.map((remote) => remote.cursor && <RemoteCursor key={remote.awarenessID} remote={remote} />)}
      </div>
      <div className="canvas-stats">{blocks.length} blocks · {visible.length} rendered</div>
    </div>
  );

  function localPoint(event: { clientX: number; clientY: number }) {
    const rect = canvas.current?.getBoundingClientRect();
    return { x: event.clientX - (rect?.left ?? 0), y: event.clientY - (rect?.top ?? 0) };
  }
}

function BlockView({ block, selected, onlySelection, readOnly, remotes, onPointerDown, onResize, onSelect, onText, onEmptyBlur }: {
  block: WhiteboardBlock;
  selected: boolean;
  onlySelection: boolean;
  readOnly: boolean;
  remotes: RemotePresence[];
  onPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  onResize: (event: ReactPointerEvent<HTMLButtonElement>, handle: ResizeHandle) => void;
  onSelect: (additive: boolean) => void;
  onText: (value: string) => void;
  onEmptyBlur: () => void;
}) {
  return (
    <article
      className={`block block-${block.type} ${selected ? 'selected' : ''}`}
      style={{
        left: block.x, top: block.y, width: block.width, height: block.height, zIndex: block.z,
        background: block.style.fill, borderColor: block.style.borderColor, borderWidth: block.style.borderWidth
      }}
      onPointerDown={block.type === 'image' ? onPointerDown : undefined}
    >
      {remotes.map((remote, index) => <div className="remote-selection" key={remote.awarenessID} style={{ borderColor: remote.color, inset: -10 - index * 5 }}><span style={{ backgroundColor: remote.color }}>{remote.name}</span></div>)}
      {selected && onlySelection && !readOnly && <ResizeHandles onResize={(event, handle) => onResize(event, handle)} />}
      {block.type === 'text' ? (
        <>
          <button className="block-drag-handle" aria-label="Move text block" onPointerDown={onPointerDown} />
          <textarea
            value={block.text}
            readOnly={readOnly}
            autoFocus={selected && block.text === ''}
            style={{ color: block.style.textColor }}
            onPointerDown={(event) => { event.stopPropagation(); onSelect(event.shiftKey); }}
            onFocus={() => onSelect(false)}
            onChange={(event) => onText(event.target.value)}
            onBlur={onEmptyBlur}
          />
        </>
      ) : <img src={imageSource(block as ImageBlock)} alt={block.alt} draggable={false} />}
    </article>
  );
}

function ResizeHandles({ onResize }: { onResize: (event: ReactPointerEvent<HTMLButtonElement>, handle: ResizeHandle) => void }) {
  const handles: ResizeHandle[] = ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw'];
  return <>{handles.map((handle) => <button key={handle} className={`resize-handle resize-${handle}`} onPointerDown={(event) => onResize(event, handle)} aria-label={`Resize ${handle}`} />)}</>;
}

function RemoteCursor({ remote }: { remote: RemotePresence }) {
  return <div className="remote-cursor" style={{ left: remote.cursor?.x, top: remote.cursor?.y, color: remote.color }}><div className="remote-cursor-tip" /><div className="remote-cursor-label" style={{ backgroundColor: remote.color }}>{remote.name}</div></div>;
}

function resizeRect(block: WhiteboardBlock, handle: ResizeHandle, start: Point, current: Point) {
  const dx = current.x - start.x;
  const dy = current.y - start.y;
  const minWidth = 60;
  const minHeight = 40;
  let { x, y } = block;
  let width = block.width;
  let height = block.height;
  if (handle.includes('e')) width = Math.max(minWidth, block.width + dx);
  if (handle.includes('s')) height = Math.max(minHeight, block.height + dy);
  if (handle.includes('w')) { width = Math.max(minWidth, block.width - dx); x = block.x + block.width - width; }
  if (handle.includes('n')) { height = Math.max(minHeight, block.height - dy); y = block.y + block.height - height; }
  if (block.type === 'image' && block.aspectLocked) {
    const ratio = block.naturalWidth / Math.max(block.naturalHeight, 1);
    const horizontal = handle.includes('e') || handle.includes('w');
    const vertical = handle.includes('n') || handle.includes('s');
    if (horizontal && !vertical) height = width / ratio;
    else if (vertical && !horizontal) width = height * ratio;
    else if (Math.abs(dx) / block.width >= Math.abs(dy) / block.height) height = width / ratio;
    else width = height * ratio;
    if (handle.includes('w')) x = block.x + block.width - width;
    if (handle.includes('n')) y = block.y + block.height - height;
  }
  return { x, y, width, height };
}

function normalizeRect(a: Point, b: Point): Rect {
  return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), width: Math.abs(a.x - b.x), height: Math.abs(a.y - b.y) };
}

function blockRect(block: WhiteboardBlock): Rect {
  return { x: block.x, y: block.y, width: block.width, height: block.height };
}

export function filterVisibleBlocks(
  blocks: WhiteboardBlock[],
  viewport: Viewport,
  canvasSize: { width: number; height: number },
  selected = new Set<string>()
) {
  const bounds: Rect = {
    x: -viewport.x / viewport.scale - 300,
    y: -viewport.y / viewport.scale - 300,
    width: canvasSize.width / viewport.scale + 600,
    height: canvasSize.height / viewport.scale + 600
  };
  return blocks.filter((block) => selected.has(block.id) || intersects(bounds, blockRect(block)));
}

function toWorld(point: Point, viewport: Viewport) {
  return { x: (point.x - viewport.x) / viewport.scale, y: (point.y - viewport.y) / viewport.scale };
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}
