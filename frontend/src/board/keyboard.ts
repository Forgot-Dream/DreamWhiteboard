import { useEffect } from 'react';
import type { BoardRuntime } from './runtime';
import { exportBlocks, importBlocks } from './commands';
import { useBoardStore } from './store';

export function useBoardKeyboard(runtime: BoardRuntime, pasteImage?: (file: File) => boolean) {
  useEffect(() => {
    function keyDown(event: KeyboardEvent) {
      const state = useBoardStore.getState();
      const selected = state.selection.ids;
      const modifier = event.ctrlKey || event.metaKey;
      const editing = isEditableTarget(event.target);

      if (event.key === 'Escape') {
        state.setSelection([]);
        state.setTool('select');
        (document.activeElement as HTMLElement | null)?.blur();
        return;
      }
      if (modifier && event.key.toLowerCase() === 'z') {
        event.preventDefault();
        event.shiftKey ? runtime.commands.redo() : runtime.commands.undo();
        return;
      }
      if (modifier && event.key.toLowerCase() === 'y') {
        event.preventDefault(); runtime.commands.redo(); return;
      }
      if (!modifier && !editing && event.key.toLowerCase() === 'v') {
        state.setTool('select'); return;
      }
      if (!modifier && !editing && runtime.canEdit && event.key.toLowerCase() === 't') {
        state.setTool('text'); return;
      }
      if (editing || !runtime.canEdit) return;
      if ((event.key === 'Delete' || event.key === 'Backspace') && selected.length) {
        event.preventDefault(); runtime.commands.delete(selected); state.setSelection([]); return;
      }
      if (modifier && event.key.toLowerCase() === 'a') {
        event.preventDefault(); state.setSelection(state.document.blocks.map((block) => block.id)); return;
      }
      if (modifier && event.key.toLowerCase() === 'd') {
        event.preventDefault(); state.setSelection(runtime.commands.duplicate(selected)); return;
      }
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key) && selected.length) {
        event.preventDefault();
        const amount = event.shiftKey ? 10 : 1;
        runtime.commands.move(selected, event.key === 'ArrowLeft' ? -amount : event.key === 'ArrowRight' ? amount : 0, event.key === 'ArrowUp' ? -amount : event.key === 'ArrowDown' ? amount : 0);
        runtime.commands.stopCapturing();
      }
    }

    function writeSelection(event: ClipboardEvent, cut: boolean) {
      if (!runtime.canEdit || isEditableTarget(event.target)) return;
      const state = useBoardStore.getState();
      const ids = state.selection.ids;
      if (ids.length === 0 || !event.clipboardData) return;
      const blocks = state.document.blocks.filter((block) => ids.includes(block.id));
      if (blocks.length === 0) return;
      const serialized = exportBlocks(blocks, runtime.projectID);
      event.preventDefault();
      event.clipboardData.setData('text/plain', serialized);
      if (cut) {
        runtime.commands.delete(ids);
        state.setSelection([]);
      }
    }

    function paste(event: ClipboardEvent) {
      if (!runtime.canEdit || isEditableTarget(event.target)) return;
      const image = clipboardImageFile(event.clipboardData);
      if (image && pasteImage) {
        if (pasteImage(image)) event.preventDefault();
        return;
      }
      const serialized = event.clipboardData?.getData('text/plain') ?? '';
      const blocks = importBlocks(serialized, runtime.projectID);
      if (blocks.length === 0) return;
      event.preventDefault();
      useBoardStore.getState().setSelection(runtime.commands.paste(blocks));
    }

    window.addEventListener('keydown', keyDown);
    const copy = (event: ClipboardEvent) => writeSelection(event, false);
    const cut = (event: ClipboardEvent) => writeSelection(event, true);
    window.addEventListener('copy', copy);
    window.addEventListener('cut', cut);
    window.addEventListener('paste', paste);
    return () => {
      window.removeEventListener('keydown', keyDown);
      window.removeEventListener('copy', copy);
      window.removeEventListener('cut', cut);
      window.removeEventListener('paste', paste);
    };
  }, [pasteImage, runtime]);
}

export function clipboardImageFile(data: DataTransfer | null): File | null {
  if (!data) return null;
  // A paste is one undoable resource action. Clipboard bitmap providers normally
  // expose a single file, so deliberately choose the first image when there are several.
  for (const item of Array.from(data.items)) {
    if (item.kind !== 'file' || !item.type.startsWith('image/')) continue;
    const image = normalizeClipboardImage(item.getAsFile(), item.type);
    if (image) return image;
  }
  for (const file of Array.from(data.files)) {
    const image = normalizeClipboardImage(file, file.type);
    if (image) return image;
  }
  return null;
}

function normalizeClipboardImage(blob: Blob | null, declaredType: string) {
  if (!blob) return null;
  const type = blob.type || declaredType || (blob instanceof File ? imageTypeFromName(blob.name) : '');
  if (!type.startsWith('image/')) return null;
  if (blob instanceof File && blob.name && blob.type === type) return blob;
  const name = blob instanceof File && blob.name ? blob.name : `clipboard-image.${imageExtension(type)}`;
  return new File([blob], name, { type, lastModified: Date.now() });
}

function imageTypeFromName(name: string) {
  const extension = name.split('.').pop()?.toLowerCase();
  if (extension === 'png') return 'image/png';
  if (extension === 'jpg' || extension === 'jpeg') return 'image/jpeg';
  if (extension === 'gif') return 'image/gif';
  if (extension === 'webp') return 'image/webp';
  return '';
}

function imageExtension(type: string) {
  if (type === 'image/jpeg') return 'jpg';
  return type.slice('image/'.length).replace(/[^a-z0-9]/gi, '') || 'png';
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || (target instanceof HTMLElement && target.isContentEditable);
}
