import { useEffect } from 'react';
import type { BoardRuntime } from './runtime';
import { exportBlocks, importBlocks } from './commands';
import { useBoardStore } from './store';

let fallbackClipboard = '';

export function useBoardKeyboard(runtime: BoardRuntime) {
  useEffect(() => {
    async function keyDown(event: KeyboardEvent) {
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
      if (modifier && event.key.toLowerCase() === 'c') {
        event.preventDefault(); await copy(selected); return;
      }
      if (modifier && event.key.toLowerCase() === 'x') {
        event.preventDefault();
        await copy(selected);
        runtime.commands.delete(selected); state.setSelection([]); return;
      }
      if (modifier && event.key.toLowerCase() === 'v') {
        event.preventDefault();
        const text = await navigator.clipboard?.readText().catch(() => fallbackClipboard) ?? fallbackClipboard;
        state.setSelection(runtime.commands.paste(importBlocks(text))); return;
      }
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key) && selected.length) {
        event.preventDefault();
        const amount = event.shiftKey ? 10 : 1;
        runtime.commands.move(selected, event.key === 'ArrowLeft' ? -amount : event.key === 'ArrowRight' ? amount : 0, event.key === 'ArrowUp' ? -amount : event.key === 'ArrowDown' ? amount : 0);
        runtime.commands.stopCapturing();
      }
    }

    async function copy(ids: string[]) {
      const blocks = useBoardStore.getState().document.blocks.filter((block) => ids.includes(block.id));
      fallbackClipboard = exportBlocks(blocks);
      await navigator.clipboard?.writeText(fallbackClipboard).catch(() => undefined);
    }

    window.addEventListener('keydown', keyDown);
    return () => window.removeEventListener('keydown', keyDown);
  }, [runtime]);
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || (target instanceof HTMLElement && target.isContentEditable);
}
