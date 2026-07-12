import { useRef } from 'react';
import {
  AlignHorizontalJustifyCenter, AlignVerticalJustifyCenter, ArrowLeft, BetweenHorizontalStart,
  BetweenVerticalStart, BringToFront, ChevronDown, ChevronUp, Copy, FileImage, Maximize2, MousePointer2, Redo2,
  RotateCcw, Scan, SendToBack, Trash2, Type, Undo2, ZoomIn, ZoomOut
} from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { useShallow } from 'zustand/react/shallow';
import type { Board, Project } from '../lib/api';
import { useI18n } from '../lib/i18n';
import type { BoardImageUpload } from './imageUpload';
import type { BoardRuntime } from './runtime';
import { blockBounds } from './schema';
import { useBoardStore, type Viewport } from './store';

export function BoardToolbar({ runtime, board, project, onBackProjectID, imageUpload }: {
  runtime: BoardRuntime;
  board: Board;
  project?: Project;
  onBackProjectID: string;
  imageUpload: BoardImageUpload;
}) {
  const { errorMessage, t } = useI18n();
  const navigate = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const selected = useBoardStore(useShallow((state) => {
    const ids = new Set(state.selection.ids);
    return state.document.blocks.filter((block) => ids.has(block.id));
  }));
  const tool = useBoardStore((state) => state.tool);
  const scale = useBoardStore((state) => state.viewport.scale);
  const connection = useBoardStore(useShallow((state) => state.connection));
  const selectedOne = selected.length === 1 ? selected[0] : undefined;

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
      <button className="icon-btn" onClick={() => navigate(`/projects/${onBackProjectID}`)} title={t('editor.back')}><ArrowLeft size={19} /></button>
      <div className="title-block"><strong>{board.name}</strong><span>{project?.name ?? t('editor.projectFallback')} · {connectionLabel(connection.state, connection.pending, connection.lastSequence, t)}</span></div>
      <div className="toolbar" role="toolbar" aria-label={t('editor.toolbarLabel')}>
        <ToolButton active={tool === 'select'} label={t('editor.select')} onClick={() => useBoardStore.getState().setTool('select')}><MousePointer2 size={18} /></ToolButton>
        <ToolButton active={tool === 'text'} disabled={!runtime.canEdit} label={t('editor.textTool')} onClick={() => useBoardStore.getState().setTool('text')}><Type size={18} /></ToolButton>
        <button className="icon-btn" disabled={!runtime.canEdit || Boolean(imageUpload.status && !imageUpload.status.error)} title={t('editor.uploadImage')} onClick={() => input.current?.click()}><FileImage size={18} /></button>
        <input ref={input} hidden type="file" accept="image/png,image/jpeg,image/gif" onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          event.currentTarget.value = '';
          if (file) void imageUpload.upload(file);
        }} />
        <span className="toolbar-divider" />
        <button className="icon-btn" disabled={!runtime.canEdit} title={t('editor.undo')} onClick={() => runtime.commands.undo()}><Undo2 size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit} title={t('editor.redo')} onClick={() => runtime.commands.redo()}><Redo2 size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title={t('editor.duplicate')} onClick={() => { const state = useBoardStore.getState(); state.setSelection(runtime.commands.duplicate(state.selection.ids)); }}><Copy size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title={t('editor.bringToFront')} onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'front')}><BringToFront size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title={t('editor.sendToBack')} onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'back')}><SendToBack size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title={t('editor.moveForward')} onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'forward')}><ChevronUp size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || !selected.length} title={t('editor.moveBackward')} onClick={() => runtime.commands.reorder(useBoardStore.getState().selection.ids, 'backward')}><ChevronDown size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 2} title={t('editor.alignHorizontal')} onClick={() => runtime.commands.align(useBoardStore.getState().selection.ids, 'horizontal')}><AlignHorizontalJustifyCenter size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 2} title={t('editor.alignVertical')} onClick={() => runtime.commands.align(useBoardStore.getState().selection.ids, 'vertical')}><AlignVerticalJustifyCenter size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 3} title={t('editor.distributeHorizontal')} onClick={() => runtime.commands.distribute(useBoardStore.getState().selection.ids, 'horizontal')}><BetweenHorizontalStart size={18} /></button>
        <button className="icon-btn" disabled={!runtime.canEdit || selected.length < 3} title={t('editor.distributeVertical')} onClick={() => runtime.commands.distribute(useBoardStore.getState().selection.ids, 'vertical')}><BetweenVerticalStart size={18} /></button>
        {selectedOne && <StyleControls runtime={runtime} block={selectedOne} />}
        <span className="toolbar-divider" />
        <button className="icon-btn" title={t('editor.zoomOut')} onClick={() => zoom(scale - 0.1)}><ZoomOut size={18} /></button>
        <span className="zoom-readout">{Math.round(scale * 100)}%</span>
        <button className="icon-btn" title={t('editor.zoomIn')} onClick={() => zoom(scale + 0.1)}><ZoomIn size={18} /></button>
        <button className="icon-btn" title={t('editor.fitAll')} onClick={() => fit()}><Maximize2 size={18} /></button>
        <button className="icon-btn" disabled={!selected.length} title={t('editor.fitSelection')} onClick={() => fit(selected)}><Scan size={18} /></button>
        <button className="icon-btn" title={t('editor.resetView')} onClick={() => useBoardStore.getState().setViewport({ x: 80, y: 80, scale: 1 })}><RotateCcw size={18} /></button>
        <button className="icon-btn danger" disabled={!runtime.canEdit || !selected.length} title={t('editor.delete')} onClick={() => { const state = useBoardStore.getState(); runtime.commands.delete(state.selection.ids); state.setSelection([]); }}><Trash2 size={18} /></button>
      </div>
      {imageUpload.status && <div className={`upload-status ${imageUpload.status.error ? 'failed' : ''}`}><span>{imageUpload.status.error || t('editor.upload.progress', { file: imageUpload.status.file.name, progress: imageUpload.status.progress })}</span>{imageUpload.status.error ? <button onClick={imageUpload.retry}>{t('editor.upload.retry')}</button> : <button onClick={imageUpload.cancel}>{t('editor.upload.cancel')}</button>}</div>}
      {connection.error && <div className="toast">{errorMessage(connection.error, 'errors.collaboration')}</div>}
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
  const { t } = useI18n();
  const ids = useBoardStore((state) => state.selection.ids);
  return (
    <div className="style-controls">
      <label title={t('editor.style.fill')}><span>{t('editor.fill')}</span><input type="color" disabled={!runtime.canEdit} value={block.style.fill === 'transparent' ? '#ffffff' : block.style.fill} onChange={(event) => runtime.commands.updateStyle(ids, { fill: event.target.value })} /></label>
      <button className="style-mini-btn" disabled={!runtime.canEdit} onClick={() => runtime.commands.updateStyle(ids, { fill: 'transparent' })}>{t('editor.noFill')}</button>
      {block.type === 'text' && <label title={t('editor.style.text')}><span>{t('editor.text')}</span><input type="color" disabled={!runtime.canEdit} value={block.style.textColor} onChange={(event) => runtime.commands.updateStyle(ids, { textColor: event.target.value })} /></label>}
      <label title={t('editor.style.border')}><span>{t('editor.border')}</span><input type="color" disabled={!runtime.canEdit} value={block.style.borderColor} onChange={(event) => runtime.commands.updateStyle(ids, { borderColor: event.target.value })} /></label>
      <label title={t('editor.style.width')}><span>{t('editor.width')}</span><input type="number" min="0" max="12" disabled={!runtime.canEdit} value={block.style.borderWidth} onChange={(event) => runtime.commands.updateStyle(ids, { borderWidth: Number(event.target.value) })} /></label>
      {block.type === 'image' && <label className="toggle-control"><input type="checkbox" checked={block.aspectLocked} disabled={!runtime.canEdit} onChange={(event) => runtime.commands.setAspectLocked(block.id, event.target.checked)} /><span>{t('editor.aspectLock')}</span></label>}
    </div>
  );
}

function connectionLabel(state: string, pending: number, sequence: number, t: ReturnType<typeof useI18n>['t']) {
  if (pending) return t(pending === 1 ? 'editor.connection.savingOne' : 'editor.connection.savingMany', { count: pending });
  if (state === 'live') return t('editor.connection.synced', { sequence });
  if (state === 'connecting') return t('editor.connection.connecting');
  if (state === 'syncing') return t('editor.connection.syncing');
  if (state === 'offline') return t('editor.connection.offline');
  return t('editor.connection.live');
}

function screenToWorld(x: number, y: number, viewport: Viewport) {
  return { x: (x - viewport.x) / viewport.scale, y: (y - viewport.y) / viewport.scale };
}
