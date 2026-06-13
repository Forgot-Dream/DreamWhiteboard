import { PointerEvent, WheelEvent, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, FileImage, GripHorizontal, MousePointer2, Square, StickyNote, Trash2, Type, ZoomIn, ZoomOut } from 'lucide-react';
import { RichTextBlock } from '../components/RichTextBlock';
import { api, assetURL, canEdit as canUserEdit, wsURL, type Board, type BoardSnapshot, type BlockType, type Project, type ProjectRole, type User, type WhiteboardBlock } from '../lib/api';
import { createBlock } from '../lib/blockRegistry';

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

export function BoardEditor({ user, board, project, role, onBack }: BoardEditorProps) {
  const [blocks, setBlocks] = useState<WhiteboardBlock[]>([]);
  const [version, setVersion] = useState(0);
  const [savedAt, setSavedAt] = useState(board.updated_at);
  const [saving, setSaving] = useState(false);
  const [connection, setConnection] = useState<'connecting' | 'live' | 'offline'>('connecting');
  const [selected, setSelected] = useState('');
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [error, setError] = useState('');
  const socket = useRef<WebSocket | null>(null);
  const saveTimers = useRef<Record<string, number>>({});
  const drag = useRef<{ mode: 'pan' | 'block'; id?: string; startX: number; startY: number; originX: number; originY: number } | null>(null);
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
        setBlocks(message.snapshot.blocks ?? []);
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
        setBlocks(message.snapshot.blocks ?? []);
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

  function addBlock(type: BlockType) {
    const block = createBlock(type, (120 - offset.x) / scale, (120 - offset.y) / scale, blocks.length + 1);
    setBlocks((current) => [...current, block]);
    setSelected(block.id);
    sendOperation('create_block', { block });
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

  function pointerMove(event: PointerEvent<HTMLDivElement>) {
    const active = drag.current;
    if (!active) return;
    if (active.mode === 'pan') {
      setOffset({ x: active.originX + event.clientX - active.startX, y: active.originY + event.clientY - active.startY });
    } else if (active.id) {
      const dx = (event.clientX - active.startX) / scale;
      const dy = (event.clientY - active.startY) / scale;
      setBlocks((current) => current.map((block) => block.id === active.id ? { ...block, x: active.originX + dx, y: active.originY + dy } : block));
    }
  }

  function pointerUp() {
    const active = drag.current;
    drag.current = null;
    if (active?.mode === 'block' && active.id) {
      const block = blocks.find((item) => item.id === active.id);
      if (block) sendOperation('move_block', { ...block });
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
    setBlocks((current) => upsert(current, block));
    sendOperation('update_block', { ...block });
  }

  function updateBlockDraft(block: WhiteboardBlock) {
    setBlocks((current) => upsert(current, block));
    window.clearTimeout(saveTimers.current[block.id]);
    saveTimers.current[block.id] = window.setTimeout(() => {
      sendOperation('update_block', { ...block });
    }, 800);
  }

  function deleteSelected() {
    const id = selected;
    if (!id) return;
    setBlocks((current) => current.filter((block) => block.id !== id));
    setSelected('');
    sendOperation('delete_block', { id });
  }

  return (
    <div className="editor-shell">
      <header className="topbar">
        <button className="icon-btn" onClick={onBack} title="Back"><ArrowLeft size={19} /></button>
        <div className="title-block">
          <strong>{board.name}</strong>
          <span>{project.name} · {saving ? 'Saving...' : `Saved ${formatSavedAt(savedAt)}`} · {connection === 'live' ? 'live' : connection}</span>
        </div>
        <div className="toolbar">
          <button className="icon-btn selected" title="Select"><MousePointer2 size={18} /></button>
          <button className="icon-btn" disabled={!canEdit} onClick={() => addBlock('note')} title="Note"><StickyNote size={18} /></button>
          <button className="icon-btn" disabled={!canEdit} onClick={() => addBlock('rich_text')} title="Rich text"><Type size={18} /></button>
          <button className="icon-btn" disabled={!canEdit} onClick={() => addBlock('shape')} title="Shape"><Square size={18} /></button>
          <label className={`icon-btn ${!canEdit ? 'disabled' : ''}`} title="Upload image">
            <FileImage size={18} />
            <input type="file" accept="image/*" disabled={!canEdit} onChange={(event) => event.target.files?.[0] && uploadImage(event.target.files[0])} />
          </label>
          <button className="icon-btn" onClick={() => setScale((value) => Math.max(0.25, value - 0.1))} title="Zoom out"><ZoomOut size={18} /></button>
          <span className="zoom-readout">{Math.round(scale * 100)}%</span>
          <button className="icon-btn" onClick={() => setScale((value) => Math.min(2.5, value + 0.1))} title="Zoom in"><ZoomIn size={18} /></button>
          <button className="icon-btn danger" disabled={!selected || !canEdit} onClick={deleteSelected} title="Delete"><Trash2 size={18} /></button>
        </div>
      </header>
      {error && <div className="toast">{error}</div>}
      <div className="canvas" onPointerDown={pointerDownCanvas} onPointerMove={pointerMove} onPointerUp={pointerUp} onWheel={wheel}>
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
            />
          ))}
        </div>
      </div>
    </div>
  );
}

function WhiteboardBlockView({ block, selected, readOnly, onDragStart, onCommit, onDraft }: {
  block: WhiteboardBlock;
  selected: boolean;
  readOnly: boolean;
  onDragStart: (event: PointerEvent<HTMLElement>) => void;
  onCommit: (block: WhiteboardBlock) => void;
  onDraft: (block: WhiteboardBlock) => void;
}) {
  const style = { left: block.x, top: block.y, width: block.w, height: block.h, zIndex: block.z };
  return (
    <div className={`block block-${block.type} ${selected ? 'selected' : ''}`} style={style}>
      <button className="block-handle" type="button" onPointerDown={onDragStart} title="Drag block">
        <GripHorizontal size={16} />
      </button>
      {block.type === 'note' && (
        <textarea
          readOnly={readOnly}
          value={String(block.data.text ?? '')}
          onPointerDown={(event) => event.stopPropagation()}
          onChange={(event) => onDraft({ ...block, data: { ...block.data, text: event.target.value } })}
          onBlur={(event) => onCommit({ ...block, data: { ...block.data, text: event.target.value } })}
        />
      )}
      {block.type === 'rich_text' && (
        <RichTextBlock
          readOnly={readOnly}
          value={block.data.doc}
          onChange={(doc) => onCommit({ ...block, data: { ...block.data, doc } })}
        />
      )}
      {block.type === 'image' && <img src={String(block.data.url ?? '')} alt={String(block.data.alt ?? '')} draggable={false} />}
      {block.type === 'shape' && <div className={`shape ${block.data.shape === 'ellipse' ? 'ellipse' : ''}`} style={{ background: String(block.data.color ?? '#2f6f73') }} />}
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

function formatSavedAt(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--';
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}
