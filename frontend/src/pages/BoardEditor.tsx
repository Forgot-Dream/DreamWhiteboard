import { PointerEvent, WheelEvent, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, FileImage, GripHorizontal, MousePointer2, Trash2, Type, ZoomIn, ZoomOut } from 'lucide-react';
import { api, assetURL, canEdit as canUserEdit, wsURL, type Board, type BoardSnapshot, type Project, type ProjectRole, type User, type WhiteboardBlock } from '../lib/api';
import { createBlock } from '../lib/blockRegistry';
import { useI18n } from '../lib/i18n';

interface BoardEditorProps {
  user: User;
  board: Board;
  project: Project;
  role?: ProjectRole;
  onBack: () => void;
}

interface SocketMessage {
  type: string;
  snapshot?: BoardSnapshot;
  operation?: { type: string; version: number; payload: Record<string, unknown>; created_at?: string };
  error?: string;
}

type Tool = 'select' | 'text';
type ResizeHandle = 'n' | 'ne' | 'e' | 'se' | 's' | 'sw' | 'w' | 'nw';
const TEXT_INSET_X = 14;
const TEXT_INSET_Y = 34;

interface DragState {
  mode: 'pan' | 'block' | 'resize';
  id?: string;
  handle?: ResizeHandle;
  startX: number;
  startY: number;
  originX: number;
  originY: number;
  originW?: number;
  originH?: number;
}

export function BoardEditor({ user, board, project, role, onBack }: BoardEditorProps) {
  const { t, formatTime } = useI18n();
  const [blocks, setBlocks] = useState<WhiteboardBlock[]>([]);
  const [version, setVersion] = useState(0);
  const [savedAt, setSavedAt] = useState(board.updated_at);
  const [saving, setSaving] = useState(false);
  const [connection, setConnection] = useState<'connecting' | 'live' | 'offline'>('connecting');
  const [tool, setTool] = useState<Tool>('select');
  const [selected, setSelected] = useState('');
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [error, setError] = useState('');
  const socket = useRef<WebSocket | null>(null);
  const saveTimers = useRef<Record<string, number>>({});
  const pendingCreates = useRef<Set<string>>(new Set());
  const drag = useRef<DragState | null>(null);
  const canEdit = canUserEdit(role, user);
  const clientID = useMemo(() => `cli_${crypto.randomUUID()}`, []);

  useEffect(() => {
    let alive = true;
    api<{ snapshot: BoardSnapshot }>(`/api/boards/${board.id}`)
      .then((result) => {
        if (!alive) return;
        setBlocks(result.snapshot.blocks ?? []);
        setVersion(result.snapshot.version);
        setSavedAt(result.snapshot.updated_at);
      })
      .catch((err) => setError(err.message));

    const ws = new WebSocket(wsURL(board.id, clientID));
    socket.current = ws;
    ws.onmessage = (event) => {
      const message = JSON.parse(event.data) as SocketMessage;
      if (message.type === 'snapshot' && message.snapshot) {
        setBlocks((current) => mergeServerBlocks(message.snapshot?.blocks ?? [], current, pendingCreates.current));
        setVersion(message.snapshot.version);
        setSavedAt(message.snapshot.updated_at);
        setSaving(false);
      }
      if (message.type === 'operation_broadcast' && message.operation) {
        applyRemoteOperation(message.operation.type, message.operation.payload);
        setVersion(message.operation.version);
        if (message.operation.created_at) setSavedAt(message.operation.created_at);
      }
      if (message.type === 'operation_ack' && message.snapshot) {
        setBlocks((current) => mergeServerBlocks(message.snapshot?.blocks ?? [], current, pendingCreates.current));
        setVersion(message.snapshot.version);
        setSavedAt(message.snapshot.updated_at);
        setSaving(false);
      }
      if (message.type === 'error') setError(message.error ?? 'Socket error');
    };
    ws.onopen = () => {
      setConnection('live');
      setError('');
      ws.send(JSON.stringify({ type: 'join', client_id: clientID }));
    };
    ws.onerror = () => setConnection('offline');
    ws.onclose = () => setConnection('offline');
    return () => {
      alive = false;
      Object.values(saveTimers.current).forEach((timer) => window.clearTimeout(timer));
      ws.close();
    };
  }, [board.id, clientID]);

  function sendOperation(operation: string, payload: Record<string, unknown>) {
    if (!canEdit || socket.current?.readyState !== WebSocket.OPEN) return;
    setSaving(true);
    socket.current.send(JSON.stringify({
      type: 'operation',
      client_id: clientID,
      op_id: `op_${crypto.randomUUID()}`,
      operation,
      base_version: version,
      payload
    }));
  }

  function applyRemoteOperation(type: string, payload: Record<string, unknown>) {
    const block = (payload.block ?? payload) as WhiteboardBlock;
    if (type === 'create_block') setBlocks((current) => upsert(current, block));
    if (type === 'update_block' || type === 'move_block' || type === 'resize_block' || type === 'reorder_block') setBlocks((current) => upsert(current, block));
    if (type === 'delete_block') setBlocks((current) => current.filter((item) => item.id !== (payload.id ?? block.id)));
  }

  function worldFromScreen(clientX: number, clientY: number) {
    return { x: (clientX - offset.x) / scale, y: (clientY - offset.y) / scale };
  }

  function addTextBlock(x = (120 - offset.x) / scale, y = (120 - offset.y) / scale) {
    const draft = createBlock('note', 0, 0, blocks.length + 1);
    const block = { ...draft, x: x - TEXT_INSET_X, y: y - TEXT_INSET_Y, data: { ...draft.data, text: '' } };
    setBlocks((current) => [...current, block]);
    setSelected(block.id);
  }

  async function uploadImage(file: File) {
    const form = new FormData();
    form.append('file', file);
    const asset = await api<{ id: string }>(`/api/projects/${project.id}/assets`, { method: 'POST', body: form });
    const block = createBlock('image', (160 - offset.x) / scale, (160 - offset.y) / scale, blocks.length + 1);
    block.data = { asset_id: asset.id, url: assetURL(asset.id), alt: file.name };
    setBlocks((current) => [...current, block]);
    sendOperation('create_block', { block });
  }

  function pointerDownCanvas(event: PointerEvent<HTMLDivElement>) {
    if (event.target !== event.currentTarget) return;
    if (tool === 'text' && canEdit) {
      const point = worldFromScreen(event.clientX, event.clientY);
      addTextBlock(point.x, point.y);
      return;
    }
    setSelected('');
    drag.current = { mode: 'pan', startX: event.clientX, startY: event.clientY, originX: offset.x, originY: offset.y };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerDownBlock(event: PointerEvent<HTMLElement>, block: WhiteboardBlock) {
    event.stopPropagation();
    if (!canEdit) {
      setSelected(block.id);
      return;
    }
    setSelected(block.id);
    drag.current = { mode: 'block', id: block.id, startX: event.clientX, startY: event.clientY, originX: block.x, originY: block.y };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerDownResize(event: PointerEvent<HTMLElement>, block: WhiteboardBlock, handle: ResizeHandle) {
    event.stopPropagation();
    if (!canEdit) return;
    setSelected(block.id);
    drag.current = {
      mode: 'resize',
      id: block.id,
      handle,
      startX: event.clientX,
      startY: event.clientY,
      originX: block.x,
      originY: block.y,
      originW: block.w,
      originH: block.h
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function pointerMove(event: PointerEvent<HTMLDivElement>) {
    const active = drag.current;
    if (!active) return;
    if (active.mode === 'pan') {
      setOffset({ x: active.originX + event.clientX - active.startX, y: active.originY + event.clientY - active.startY });
    } else if (active.id) {
      setBlocks((current) => current.map((block) => block.id === active.id ? transformDraggedBlock(block, active, event.clientX, event.clientY, scale) : block));
    }
  }

  function pointerUp(event: PointerEvent<HTMLDivElement>) {
    const active = drag.current;
    drag.current = null;
    if (active?.mode === 'block' && active.id) {
      const block = blocks.find((item) => item.id === active.id);
      if (block) sendOperation('move_block', { ...transformDraggedBlock(block, active, event.clientX, event.clientY, scale) });
    }
    if (active?.mode === 'resize' && active.id) {
      const block = blocks.find((item) => item.id === active.id);
      if (block) sendOperation('resize_block', { ...transformDraggedBlock(block, active, event.clientX, event.clientY, scale) });
    }
  }

  function wheel(event: WheelEvent<HTMLDivElement>) {
    event.preventDefault();
    const next = Math.min(2.5, Math.max(0.25, scale + (event.deltaY > 0 ? -0.08 : 0.08)));
    const before = worldFromScreen(event.clientX, event.clientY);
    setScale(next);
    setOffset({ x: event.clientX - before.x * next, y: event.clientY - before.y * next });
  }

  function updateBlock(block: WhiteboardBlock) {
    window.clearTimeout(saveTimers.current[block.id]);
    if (shouldDeleteEmptyTextBlock(block)) {
      removeBlock(block.id);
      return;
    }
    const wasPending = shouldCreateOnSync(block, pendingCreates.current);
    const next = cleanLocalCreateState(block);
    setBlocks((current) => upsert(current, next));
    if (wasPending) pendingCreates.current.add(block.id);
    sendOperation(wasPending ? 'create_block' : 'update_block', wasPending ? { block: cleanBlockForSync(next) } : { ...next });
  }

  function updateBlockDraft(block: WhiteboardBlock) {
    const wasPending = shouldCreateOnSync(block, pendingCreates.current);
    const next = wasPending ? block : cleanLocalCreateState(block);
    setBlocks((current) => upsert(current, next));
    window.clearTimeout(saveTimers.current[block.id]);
    saveTimers.current[block.id] = window.setTimeout(() => {
      if (shouldDeleteEmptyTextBlock(next)) return;
      if (wasPending) {
        pendingCreates.current.add(next.id);
        const synced = cleanLocalCreateState(next);
        setBlocks((current) => upsert(current, synced));
        sendOperation('create_block', { block: cleanBlockForSync(synced) });
        return;
      }
      sendOperation('update_block', { ...next });
    }, 800);
  }

  function deleteSelected() {
    const id = selected;
    if (!id) return;
    removeBlock(id);
  }

  function removeBlock(id: string) {
    window.clearTimeout(saveTimers.current[id]);
    const shouldSyncDelete = !blocks.some((block) => block.id === id && isPendingTextBlock(block) && !pendingCreates.current.has(id));
    pendingCreates.current.delete(id);
    setBlocks((current) => current.filter((block) => block.id !== id));
    setSelected((current) => current === id ? '' : current);
    if (shouldSyncDelete) sendOperation('delete_block', { id });
  }

  const selectedBlock = blocks.find((block) => block.id === selected);

  return (
    <div className="editor-shell">
      <header className="topbar">
        <button className="icon-btn" onClick={onBack} title={t('editor.back')}><ArrowLeft size={19} /></button>
        <div className="title-block">
          <strong>{board.name}</strong>
          <span>{project.name} · {saving ? t('editor.saving') : t('editor.saved', { time: formatSavedAt(savedAt, formatTime) })} · {t(`editor.connection.${connection}`)}</span>
        </div>
        <div className="toolbar">
          <button className={`icon-btn ${tool === 'select' ? 'selected' : ''}`} onClick={() => setTool('select')} title={t('editor.select')}><MousePointer2 size={18} /></button>
          <button className={`icon-btn ${tool === 'text' ? 'selected' : ''}`} disabled={!canEdit} onClick={() => setTool('text')} title={t('editor.textBox')}><Type size={18} /></button>
          <label className={`icon-btn ${!canEdit ? 'disabled' : ''}`} title={t('editor.uploadImage')}>
            <FileImage size={18} />
            <input type="file" accept="image/*" disabled={!canEdit} onChange={(event) => event.target.files?.[0] && uploadImage(event.target.files[0])} />
          </label>
          {selectedBlock && selectedBlock.type !== 'image' && (
            <BlockStyleControls block={selectedBlock} disabled={!canEdit} onChange={updateBlock} />
          )}
          <button className="icon-btn" onClick={() => setScale((value) => Math.max(0.25, value - 0.1))} title={t('editor.zoomOut')}><ZoomOut size={18} /></button>
          <span className="zoom-readout">{Math.round(scale * 100)}%</span>
          <button className="icon-btn" onClick={() => setScale((value) => Math.min(2.5, value + 0.1))} title={t('editor.zoomIn')}><ZoomIn size={18} /></button>
          <button className="icon-btn danger" disabled={!selected || !canEdit} onClick={deleteSelected} title={t('editor.delete')}><Trash2 size={18} /></button>
        </div>
      </header>
      {error && <div className="toast">{error}</div>}
      <div className={`canvas ${tool === 'text' && canEdit ? 'canvas-text-tool' : ''}`} onPointerDown={pointerDownCanvas} onPointerMove={pointerMove} onPointerUp={pointerUp} onWheel={wheel}>
        <div className="world" style={{ transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})` }}>
          {blocks.map((block) => (
            <WhiteboardBlockView
              key={block.id}
              block={block}
              selected={selected === block.id}
              readOnly={!canEdit}
              onDragStart={(event) => pointerDownBlock(event, block)}
              onCommit={updateBlock}
              onDraft={updateBlockDraft}
              onResizeStart={(event, handle) => pointerDownResize(event, block, handle)}
              dragTitle={t('editor.dragBlock')}
              autoFocus={selected === block.id && isPendingTextBlock(block)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

function WhiteboardBlockView({ block, selected, readOnly, onDragStart, onResizeStart, onCommit, onDraft, dragTitle, autoFocus }: {
  block: WhiteboardBlock;
  selected: boolean;
  readOnly: boolean;
  onDragStart: (event: PointerEvent<HTMLElement>) => void;
  onResizeStart: (event: PointerEvent<HTMLElement>, handle: ResizeHandle) => void;
  onCommit: (block: WhiteboardBlock) => void;
  onDraft: (block: WhiteboardBlock) => void;
  dragTitle: string;
  autoFocus: boolean;
}) {
  const style = {
    left: block.x,
    top: block.y,
    width: block.w,
    height: block.h,
    zIndex: block.z,
    background: blockFill(block),
    borderColor: blockBorderColor(block),
    borderWidth: blockBorderWidth(block),
    boxShadow: blockShadow(block)
  };
  const textStyle = { color: blockTextColor(block) };
  return (
    <div className={`block block-${block.type} ${selected ? 'selected' : ''}`} style={style}>
      {selected && block.type !== 'image' && !readOnly && (
        <ResizeHandles onResizeStart={onResizeStart} />
      )}
      <button className="block-handle" type="button" onPointerDown={onDragStart} title={dragTitle}>
        <GripHorizontal size={16} />
      </button>
      {block.type !== 'image' && (
        <textarea
          readOnly={readOnly}
          value={blockText(block)}
          style={textStyle}
          autoFocus={autoFocus}
          onPointerDown={(event) => event.stopPropagation()}
          onChange={(event) => onDraft({ ...block, data: { ...block.data, text: event.target.value } })}
          onBlur={(event) => onCommit({ ...block, data: { ...block.data, text: event.target.value } })}
        />
      )}
      {block.type === 'image' && <img src={String(block.data.url ?? '')} alt={String(block.data.alt ?? '')} draggable={false} />}
    </div>
  );
}

function ResizeHandles({ onResizeStart }: { onResizeStart: (event: PointerEvent<HTMLElement>, handle: ResizeHandle) => void }) {
  const handles: ResizeHandle[] = ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw'];
  return (
    <>
      {handles.map((handle) => (
        <button
          className={`resize-handle resize-${handle}`}
          key={handle}
          type="button"
          onPointerDown={(event) => onResizeStart(event, handle)}
          aria-label={`resize ${handle}`}
        />
      ))}
    </>
  );
}

function BlockStyleControls({ block, disabled, onChange }: {
  block: WhiteboardBlock;
  disabled: boolean;
  onChange: (block: WhiteboardBlock) => void;
}) {
  const { t } = useI18n();
  function update(data: Record<string, unknown>) {
    onChange({ ...block, data: { ...block.data, ...data } });
  }
  return (
    <div className="style-controls">
      <label title={t('editor.style.fill')}>
        <span>{t('editor.fill')}</span>
        <input type="color" disabled={disabled} value={blockFillInput(block)} onChange={(event) => update({ fill: event.target.value })} />
      </label>
      <button className="style-mini-btn" type="button" disabled={disabled} onClick={() => update({ fill: 'transparent' })}>
        {t('editor.noFill')}
      </button>
      <label title={t('editor.style.text')}>
        <span>{t('editor.text')}</span>
        <input type="color" disabled={disabled} value={blockTextColor(block)} onChange={(event) => update({ textColor: event.target.value })} />
      </label>
      <label title={t('editor.style.border')}>
        <span>{t('editor.border')}</span>
        <input type="color" disabled={disabled} value={blockBorderColor(block)} onChange={(event) => update({ borderColor: event.target.value })} />
      </label>
      <label title={t('editor.style.width')}>
        <span>{t('editor.width')}</span>
        <input type="number" min="0" max="12" disabled={disabled} value={blockBorderWidth(block)} onChange={(event) => update({ borderWidth: Number(event.target.value) })} />
      </label>
    </div>
  );
}

function upsert(blocks: WhiteboardBlock[], block: WhiteboardBlock) {
  const index = blocks.findIndex((item) => item.id === block.id);
  if (index === -1) return [...blocks, block];
  const next = blocks.slice();
  next[index] = block;
  return next;
}

function mergeServerBlocks(serverBlocks: WhiteboardBlock[], currentBlocks: WhiteboardBlock[], pendingCreates: Set<string>) {
  const serverIDs = new Set(serverBlocks.map((block) => block.id));
  for (const id of Array.from(pendingCreates)) {
    if (serverIDs.has(id)) pendingCreates.delete(id);
  }
  const localDrafts = currentBlocks.filter((block) => (isPendingTextBlock(block) || pendingCreates.has(block.id)) && !serverIDs.has(block.id));
  return [...serverBlocks, ...localDrafts];
}

function transformDraggedBlock(block: WhiteboardBlock, active: DragState, clientX: number, clientY: number, scale: number) {
  const dx = (clientX - active.startX) / scale;
  const dy = (clientY - active.startY) / scale;
  if (active.mode === 'block') {
    return { ...block, x: active.originX + dx, y: active.originY + dy };
  }
  if (active.mode !== 'resize' || !active.handle) return block;

  const minW = 80;
  const minH = 54;
  let x = active.originX;
  let y = active.originY;
  let w = active.originW ?? block.w;
  let h = active.originH ?? block.h;

  if (active.handle.includes('e')) w = Math.max(minW, (active.originW ?? block.w) + dx);
  if (active.handle.includes('s')) h = Math.max(minH, (active.originH ?? block.h) + dy);
  if (active.handle.includes('w')) {
    const nextW = Math.max(minW, (active.originW ?? block.w) - dx);
    x = active.originX + ((active.originW ?? block.w) - nextW);
    w = nextW;
  }
  if (active.handle.includes('n')) {
    const nextH = Math.max(minH, (active.originH ?? block.h) - dy);
    y = active.originY + ((active.originH ?? block.h) - nextH);
    h = nextH;
  }
  return { ...block, x, y, w, h };
}

function blockText(block: WhiteboardBlock) {
  if (typeof block.data.text === 'string') return block.data.text;
  if (block.type === 'rich_text') return extractRichText(block.data.doc);
  return '';
}

function shouldDeleteEmptyTextBlock(block: WhiteboardBlock) {
  return isPendingTextBlock(block) && blockText(block).trim() === '';
}

function isPendingTextBlock(block: WhiteboardBlock) {
  return block.type !== 'image' && Boolean(block.data.pendingCreate);
}

function shouldCreateOnSync(block: WhiteboardBlock, pendingCreates: Set<string>) {
  return isPendingTextBlock(block) && !pendingCreates.has(block.id);
}

function cleanLocalCreateState(block: WhiteboardBlock) {
  return { ...block, data: { ...block.data, pendingCreate: false } };
}

function cleanBlockForSync(block: WhiteboardBlock) {
  const { pendingCreate, ...data } = block.data;
  void pendingCreate;
  return { ...block, data };
}

function extractRichText(doc: unknown): string {
  if (!doc || typeof doc !== 'object') return '';
  const node = doc as { text?: unknown; content?: unknown };
  const ownText = typeof node.text === 'string' ? node.text : '';
  const children = Array.isArray(node.content) ? node.content.map(extractRichText).filter(Boolean).join('\n') : '';
  return [ownText, children].filter(Boolean).join('\n');
}

function blockFill(block: WhiteboardBlock) {
  return stringData(block, 'fill', 'transparent');
}

function blockFillInput(block: WhiteboardBlock) {
  const fill = blockFill(block);
  return fill === 'transparent' ? '#ffffff' : fill;
}

function blockTextColor(block: WhiteboardBlock) {
  return stringData(block, 'textColor', '#3b3220');
}

function blockBorderColor(block: WhiteboardBlock) {
  return stringData(block, 'borderColor', '#becbc7');
}

function blockBorderWidth(block: WhiteboardBlock) {
  const value = block.data.borderWidth;
  return typeof value === 'number' && Number.isFinite(value) ? value : 1;
}

function blockShadow(block: WhiteboardBlock) {
  return block.type === 'image' ? '0 12px 28px rgba(28, 44, 43, 0.14)' : 'none';
}

function stringData(block: WhiteboardBlock, key: string, fallback: string) {
  const value = block.data[key];
  return typeof value === 'string' ? value : fallback;
}

function formatSavedAt(value: string, formatTime: (value: string | Date) => string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--';
  return formatTime(date);
}
