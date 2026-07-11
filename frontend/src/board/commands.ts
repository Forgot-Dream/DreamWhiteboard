import * as Y from 'yjs';
import { blockToYMap, blocksMap, createImageBlock, createTextBlock, readBlocks, type BlockStyle, type ImageBlock, type WhiteboardBlock } from './schema';

export interface ImageInput {
  x: number; y: number; assetId: string; alt: string;
  naturalWidth: number; naturalHeight: number; width: number; height: number;
}

export class BoardCommands {
  readonly undoManager: Y.UndoManager;
  private readonly origin = { kind: 'local-board-command' };

  constructor(private readonly doc: Y.Doc, private readonly canEdit: () => boolean) {
    this.undoManager = new Y.UndoManager(blocksMap(doc), {
      trackedOrigins: new Set([this.origin]),
      captureTimeout: 450
    });
  }

  createText(x: number, y: number) {
    if (!this.canEdit()) return '';
    const block = createTextBlock(x, y, this.maxZ() + 1);
    this.insert([block]);
    return block.id;
  }

  createImage(input: ImageInput) {
    if (!this.canEdit()) return '';
    const block = createImageBlock({ ...input, z: this.maxZ() + 1 });
    this.insert([block]);
    return block.id;
  }

  insert(blocks: WhiteboardBlock[]) {
    if (!this.canEdit() || blocks.length === 0) return [];
    const ids: string[] = [];
    this.transact(() => {
      for (const source of blocks) {
        const block = { ...source, style: { ...source.style }, id: source.id || `blk_${crypto.randomUUID()}` } as WhiteboardBlock;
        blocksMap(this.doc).set(block.id, blockToYMap(block));
        ids.push(block.id);
      }
    });
    return ids;
  }

  replaceText(id: string, value: string) {
    if (!this.canEdit()) return;
    const map = blocksMap(this.doc).get(id);
    const text = map?.get('text');
    if (!(text instanceof Y.Text)) return;
    const current = text.toString();
    if (current === value) return;
    let prefix = 0;
    while (prefix < current.length && prefix < value.length && current[prefix] === value[prefix]) prefix += 1;
    let suffix = 0;
    while (
      suffix < current.length - prefix && suffix < value.length - prefix &&
      current[current.length - 1 - suffix] === value[value.length - 1 - suffix]
    ) suffix += 1;
    this.transact(() => {
      const deleteLength = current.length - prefix - suffix;
      if (deleteLength > 0) text.delete(prefix, deleteLength);
      const inserted = value.slice(prefix, value.length - suffix);
      if (inserted) text.insert(prefix, inserted);
    });
  }

  move(ids: string[], dx: number, dy: number) {
    if (!this.canEdit() || (!dx && !dy)) return;
    this.transact(() => {
      for (const id of ids) {
        const map = blocksMap(this.doc).get(id);
        if (!map) continue;
        map.set('x', number(map.get('x')) + dx);
        map.set('y', number(map.get('y')) + dy);
      }
    });
  }

  resize(id: string, rect: { x: number; y: number; width: number; height: number }) {
    if (!this.canEdit()) return;
    const map = blocksMap(this.doc).get(id);
    if (!map) return;
    this.transact(() => {
      map.set('x', rect.x); map.set('y', rect.y);
      map.set('width', Math.max(24, rect.width)); map.set('height', Math.max(24, rect.height));
    });
  }

  updateStyle(ids: string[], patch: Partial<BlockStyle>) {
    if (!this.canEdit()) return;
    this.transact(() => {
      for (const id of ids) {
        const map = blocksMap(this.doc).get(id);
        if (!map) continue;
        const currentStyle = map.get('style');
        const styleMap: Y.Map<unknown> = currentStyle instanceof Y.Map ? currentStyle as Y.Map<unknown> : new Y.Map<unknown>();
        if (!(currentStyle instanceof Y.Map)) map.set('style', styleMap);
        if (patch.fill !== undefined) styleMap.set('fill', patch.fill);
        if (patch.textColor !== undefined) styleMap.set('text_color', patch.textColor);
        if (patch.borderColor !== undefined) styleMap.set('border_color', patch.borderColor);
        if (patch.borderWidth !== undefined) styleMap.set('border_width', patch.borderWidth);
      }
    });
  }

  setAspectLocked(id: string, locked: boolean) {
    if (!this.canEdit()) return;
    const image = blocksMap(this.doc).get(id)?.get('image');
    if (!(image instanceof Y.Map)) return;
    this.transact(() => image.set('aspect_locked', locked));
  }

  delete(ids: string[]) {
    if (!this.canEdit() || ids.length === 0) return;
    this.transact(() => ids.forEach((id) => blocksMap(this.doc).delete(id)));
  }

  duplicate(ids: string[], offset = 24) {
    if (!this.canEdit()) return [];
    const selected = readBlocks(this.doc).filter((block) => ids.includes(block.id));
    const maxZ = this.maxZ();
    const clones = selected.map((block, index) => ({
      ...block,
      id: `blk_${crypto.randomUUID()}`,
      x: block.x + offset,
      y: block.y + offset,
      z: maxZ + index + 1,
      style: { ...block.style }
    } as WhiteboardBlock));
    return this.insert(clones);
  }

  paste(blocks: WhiteboardBlock[], offset = 24) {
    const maxZ = this.maxZ();
    return this.insert(blocks.map((block, index) => ({
      ...block,
      id: `blk_${crypto.randomUUID()}`,
      x: block.x + offset,
      y: block.y + offset,
      z: maxZ + index + 1,
      style: { ...block.style }
    } as WhiteboardBlock)));
  }

  reorder(ids: string[], direction: 'front' | 'back' | 'forward' | 'backward') {
    if (!this.canEdit() || ids.length === 0) return;
    const ordered = readBlocks(this.doc);
    const selected = new Set(ids);
    if (direction === 'front') ordered.sort((a, b) => Number(selected.has(a.id)) - Number(selected.has(b.id)) || a.z - b.z);
    if (direction === 'back') ordered.sort((a, b) => Number(selected.has(b.id)) - Number(selected.has(a.id)) || a.z - b.z);
    if (direction === 'forward') {
      for (let index = ordered.length - 2; index >= 0; index -= 1) if (selected.has(ordered[index].id) && !selected.has(ordered[index + 1].id)) [ordered[index], ordered[index + 1]] = [ordered[index + 1], ordered[index]];
    }
    if (direction === 'backward') {
      for (let index = 1; index < ordered.length; index += 1) if (selected.has(ordered[index].id) && !selected.has(ordered[index - 1].id)) [ordered[index], ordered[index - 1]] = [ordered[index - 1], ordered[index]];
    }
    this.transact(() => ordered.forEach((block, index) => blocksMap(this.doc).get(block.id)?.set('z', index + 1)));
  }

  align(ids: string[], axis: 'horizontal' | 'vertical') {
    if (!this.canEdit() || ids.length < 2) return;
    const selected = readBlocks(this.doc).filter((block) => ids.includes(block.id));
    if (axis === 'horizontal') {
      const center = selected.reduce((sum, block) => sum + block.y + block.height / 2, 0) / selected.length;
      this.transact(() => selected.forEach((block) => blocksMap(this.doc).get(block.id)?.set('y', center - block.height / 2)));
    } else {
      const center = selected.reduce((sum, block) => sum + block.x + block.width / 2, 0) / selected.length;
      this.transact(() => selected.forEach((block) => blocksMap(this.doc).get(block.id)?.set('x', center - block.width / 2)));
    }
  }

  distribute(ids: string[], axis: 'horizontal' | 'vertical') {
    if (!this.canEdit() || ids.length < 3) return;
    const selected = readBlocks(this.doc).filter((block) => ids.includes(block.id));
    const key = axis === 'horizontal' ? 'x' : 'y';
    const size = axis === 'horizontal' ? 'width' : 'height';
    selected.sort((a, b) => a[key] - b[key]);
    const total = selected.reduce((sum, block) => sum + block[size], 0);
    const start = selected[0][key];
    const end = selected[selected.length - 1][key] + selected[selected.length - 1][size];
    const gap = (end - start - total) / (selected.length - 1);
    this.transact(() => {
      let position = start;
      for (const block of selected) { blocksMap(this.doc).get(block.id)?.set(key, position); position += block[size] + gap; }
    });
  }

  undo() { if (this.canEdit()) this.undoManager.undo(); }
  redo() { if (this.canEdit()) this.undoManager.redo(); }
  stopCapturing() { this.undoManager.stopCapturing(); }
  destroy() { this.undoManager.destroy(); }

  private maxZ() {
    return readBlocks(this.doc).reduce((max, block) => Math.max(max, block.z), 0);
  }

  private transact(callback: () => void) {
    this.doc.transact(callback, this.origin);
  }
}

export function exportBlocks(blocks: WhiteboardBlock[]) {
  return JSON.stringify({ type: 'dreamwhiteboard/blocks', version: 1, blocks });
}

export function importBlocks(value: string): WhiteboardBlock[] {
  try {
    const parsed = JSON.parse(value) as { type?: string; version?: number; blocks?: WhiteboardBlock[] };
    if (parsed.type !== 'dreamwhiteboard/blocks' || parsed.version !== 1 || !Array.isArray(parsed.blocks)) return [];
    return parsed.blocks.filter(validBlock);
  } catch {
    return [];
  }
}

function validBlock(block: WhiteboardBlock) {
  return Boolean(block && typeof block.id === 'string' && (block.type === 'text' || block.type === 'image') && Number.isFinite(block.x) && Number.isFinite(block.y));
}

function number(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}
