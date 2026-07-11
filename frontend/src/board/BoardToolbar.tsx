import { useRef, useState } from 'react';
import {
  AlignHorizontalJustifyCenter, AlignVerticalJustifyCenter, ArrowLeft, BetweenHorizontalStart,
  BetweenVerticalStart, BringToFront, ChevronDown, ChevronUp, Copy, FileImage, Hand, Maximize2, MousePointer2, Redo2,
  RotateCcw, Scan, SendToBack, Trash2, Type, Undo2, ZoomIn, ZoomOut
} from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { useShallow } from 'zustand/react/shallow';
import { uploadAsset, type Board, type Project } from '../lib/api';
import type { BoardRuntime } from './runtime';
import { blockBounds } from './schema';
import { useBoardStore, type Viewport } from './store';

const MAX_CLIENT_UPLOAD = 25 * 1024 * 1024;
const ALLOWED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif']);

export function BoardToolbar({ runtime, board, project, onBackProjectID }: { runtime: BoardRuntime; board: Board; project?: Project; onBackProjectID: string }) {
  const navigate = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const [upload, setUpload] = useState<{ file: File; progress: number; error: string; controller: AbortController } | null>(null);
  const selected = useBoardStore(useShallow((state) => {
    const ids = new Set(state.selection.ids);
    return state.document.blocks.filter((block) => ids.has(block.id));
  }));
  const tool = useBoardStore((state) => state.tool);
  const scale = useBoardStore((state) => state.viewport.scale);
  const connection = useBoardStore(useShallow((state) => state.connection));
  const selectedOne = selected.length === 1 ? selected[0] : undefined;

  async function chooseFile(file: File) {
    if (!ALLOWED_IMAGE_TYPES.has(file.type)) { setUpload({ file, progress: 0, error: 'Choose a PNG, JPEG, or GIF image.', controller: new AbortController() }); return; }
    if (file.size > MAX_CLIENT_UPLOAD) { setUpload({ file, progress: 0, error: 'Image exceeds the 25 MB upload limit.', controller: new AbortController() }); return; }
    const controller = new AbortController();
    setUpload({ file, progress: 0, error: '', controller });
    try {
      const dimensions = await readImageDimensions(file);
      const asset = await uploadAsset(board.project_id, file, {
        signal: controller.signal,
        onProgress: (progress) => setUpload((current) => current ? { ...current, progress } : null)
      });
      const size = fitImageSize(asset.width ?? dimensions.width, asset.height ?? dimensions.height);
      const viewport = useBoardStore.getState().viewport;
      const center = screenToWorld(window.innerWidth / 2, (window.innerHeight - 58) / 2, viewport);
      const id = runtime.commands.createImage({
        x: center.x - size.width / 2, y: center.y - size.height / 2,
        assetId: asset.id, alt: file.name,
        naturalWidth: asset.width ?? dimensions.width, naturalHeight: asset.height ?? dimensions.height,
        width: size.width, height: size.height
      });
      if (id) useBoardStore.getState().setSelection([id]);
      setUpload(null);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') { setUpload(null); return; }
      setUpload((current) => current ? { ...current, error: error instanceof Error ? error.message : 'Upload failed' } : null);
    } finally {
      if (input.current) input.current.value = '';
    }
  }

  function fit(blocks = useBoardStore.getState().document.blocks) {
    const bounds = blockBounds(blocks);
    if (!bounds) return;
    const width = window.innerWidth;
    const height = window.innerHeight - 58;
    const scale = Math.min(2.5, Math.max(0.15, Math.min((width - 120) / Math.max(bounds.width, 1), (height - 120) / Math.max(bounds.height, 1))));
    useBoardStore.getState().setViewport({ x: width / 2 - (bounds.x + bounds.width / 2) * scale, y: height / 2 - (bounds.y + bounds.height / 2) * scale, scale });
  }

  return (
    <header className="topbar">
      <button className="icon-btn" onClick={() => navigate(`/projects/${onBackProjectID}`)} title="Back"><ArrowLeft size={19} /></button>
      <div className="title-block"><strong>{board.name}</strong><span>{project?.name ?? 'Project'} · {connectionLabel(connection.state, connection.pending, connection.lastSequence)}</span></div>
      <div className="toolbar" role="toolbar" aria-label="Whiteboard tools">
        <ToolButton active={tool === 'select'} label="Select (V)" onClick={() => useBoardStore.getState().setTool('select')}><MousePointer2 size={18} /></ToolButton>
        <ToolButton active={tool === 'pan'} label="Pan" onClick={() => useBoardStore.getState().setTool('pan')}><Hand size={18} /></ToolButton>
        <ToolButton active={tool === 'text'} disabled={!runtime.canEdit} label="Text" onClick={() => useBoardStore.getState().setTool('text')}><Type size={18} /></ToolButton>
        <button className="icon-btn" disabled={!runtime.canEdit || Boolean(upload && !upload.error)} title="Upload image" onClick={() => input.current?.click()}><FileImage size={18} /></button>
        <input ref={input} hidden type="file" accept="image/png,image/jpeg,image/gif" onChange={(event) => event.target.files?.[0] && chooseFile(event.target.files[0])} />
        <span className="toolbar-divider" />
        <button className="icon-btn" disabled={!runtime.canEdit} title="Undo" onClick={() => runtime.commands.undo()}><Undo2 size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit} title="Redo" onClick={() => runtime.commands.redo()}><Redo2 size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title="Duplicate" onClick={() => { const state = useBoardStore.getState(); state.setSelection(runtime.commands.duplicate(state.selection.ids)); }}><Copy size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title="Bring to front" onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'front')}><BringToFront size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title="Send to back" onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'back')}><SendToBack size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title="Move forward" onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'forward')}><ChevronUp size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title="Move backward" onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'backward')}><ChevronDown size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 2} title="Align horizontal centers" onClick={() => runtime.commands.align(useBoardStore.getState().selection.ids, 'horizontal')}><AlignHorizontalJustifyCenter size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 2} title="Align vertical centers" onClick={() => runtime.commands.align(useBoardStore.getState().selection.ids, 'vertical')}><AlignVerticalJustifyCenter size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 3} title="Distribute horizontally" onClick={() => runtime.commands.distribute(useBoardStore.getState().selection.ids, 'horizontal')}><BetweenHorizontalStart size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 3} title="Distribute vertically" onClick={() => runtime.commands.distribute(useBoardStore.getState().selection.ids, 'vertical')}><BetweenVerticalStart size={18} /></button>
        {selectedOne && <StyleControls runtime={runtime} block={selectedOne} />}
        <span className="toolbar-divider" />
        <button className="icon-btn" title="Zoom out" onClick={() => zoom(scale - 0.1)}><ZoomOut size={18} /></button>
        <span className="zoom-readout">{Math.round(scale * 100)}%</span>
        <button className="icon-btn" title="Zoom in" onClick={() => zoom(scale + 0.1)}><ZoomIn size={18} /></button>
        <button className="icon-btn" title="Fit all" onClick={() => fit()}><Maximize2 size={18} /></button>
        <button className="icon-btn" disabled={!selected.length} title="Fit selection" onClick={() => fit(selected)}><Scan size={18} /></button>
        <button className="icon-btn" title="Reset view" onClick={() => useBoardStore.getState().setViewport({ x: 80, y: 80, scale: 1 })}><RotateCcw size={18} /></button>
        <button className="icon-btn danger" disabled={!runtime.canEdit || !selected.length} title="Delete" onClick={() => { const state = useBoardStore.getState(); runtime.commands.delete(state.selection.ids); state.setSelection([]); }}><Trash2 size={18} /></button>
      </div>
      {upload && <div className={`upload-status ${upload.error ? 'failed' : ''}`}><span>{upload.error || `Uploading ${upload.file.name}: ${upload.progress}%`}</span>{upload.error ? <button onClick={() => chooseFile(upload.file)}>Retry</button> : <button onClick={() => upload.controller.abort()}>Cancel</button>}</div>}
      {connection.error && <div className="toast">{connection.error}</div>}
    </header>
  );

  function zoom(next: number) {
    const scale = Math.min(4, Math.max(0.15, next));
    const cx = window.innerWidth / 2;
    const cy = (window.innerHeight - 58) / 2;
    const state = useBoardStore.getState();
    const world = screenToWorld(cx, cy, state.viewport);
    state.setViewport({ x: cx - world.x * scale, y: cy - world.y * scale, scale });
  }
}

function ToolButton({ active, disabled, label, onClick, children }: { active: boolean; disabled?: boolean; label: string; onClick: () => void; children: React.ReactNode }) {
  return <button className={`icon-btn ${active ? 'selected' : ''}`} disabled={disabled} title={label} onClick={onClick}>{children}</button>;
}

function StyleControls({ runtime, block }: { runtime: BoardRuntime; block: ReturnType<typeof useBoardStore.getState>['document']['blocks'][number] }) {
  const ids = useBoardStore((state) => state.selection.ids);
  return (
    <div className="style-controls">
      <label title="Fill"><span>Fill</span><input type="color" disabled={!runtime.canEdit} value={block.style.fill === 'transparent' ? '#ffffff' : block.style.fill} onChange={(event) => runtime.commands.updateStyle(ids, { fill: event.target.value })} /></label>
      <button className="style-mini-btn" disabled={!runtime.canEdit} onClick={() => runtime.commands.updateStyle(ids, { fill: 'transparent' })}>None</button>
      {block.type === 'text' && <label title="Text"><span>Text</span><input type="color" disabled={!runtime.canEdit} value={block.style.textColor} onChange={(event) => runtime.commands.updateStyle(ids, { textColor: event.target.value })} /></label>}
      <label title="Border"><span>Border</span><input type="color" disabled={!runtime.canEdit} value={block.style.borderColor} onChange={(event) => runtime.commands.updateStyle(ids, { borderColor: event.target.value })} /></label>
      <label title="Border width"><span>Width</span><input type="number" min="0" max="12" disabled={!runtime.canEdit} value={block.style.borderWidth} onChange={(event) => runtime.commands.updateStyle(ids, { borderWidth: Number(event.target.value) })} /></label>
      {block.type === 'image' && <label className="toggle-control"><input type="checkbox" checked={block.aspectLocked} disabled={!runtime.canEdit} onChange={(event) => runtime.commands.setAspectLocked(block.id, event.target.checked)} /><span>Lock ratio</span></label>}
    </div>
  );
}

function connectionLabel(state: string, pending: number, sequence: number) {
  if (pending) return `saving ${pending} update${pending === 1 ? '' : 's'}`;
  if (state === 'live') return `synced · seq ${sequence}`;
  return state;
}

function screenToWorld(x: number, y: number, viewport: Viewport) {
  return { x: (x - viewport.x) / viewport.scale, y: (y - viewport.y) / viewport.scale };
}

function readImageDimensions(file: File) {
  return new Promise<{ width: number; height: number }>((resolve, reject) => {
    const image = new Image();
    const url = URL.createObjectURL(file);
    image.onload = () => { URL.revokeObjectURL(url); resolve({ width: image.naturalWidth || 320, height: image.naturalHeight || 220 }); };
    image.onerror = () => { URL.revokeObjectURL(url); reject(new Error('The image could not be decoded.')); };
    image.src = url;
  });
}

function fitImageSize(width: number, height: number) {
  const ratio = Math.min(1, 560 / Math.max(width, 1), 400 / Math.max(height, 1));
  return { width: Math.max(80, Math.round(width * ratio)), height: Math.max(54, Math.round(height * ratio)) };
}
