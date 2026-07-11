import { describe, expect, it } from 'vitest';
import * as Y from 'yjs';
import { BoardCommands, exportBlocks, importBlocks } from './commands';
import { blocksMap, readBlocks } from './schema';

describe('BoardCommands', () => {
  it('uses an explicit text/image schema and supports local undo/redo', () => {
    const doc = new Y.Doc();
    const commands = new BoardCommands(doc, () => true);
    const id = commands.createText(10, 20);
    commands.stopCapturing();
    commands.replaceText(id, 'hello');
    expect(readBlocks(doc)[0]).toMatchObject({ id, type: 'text', text: 'hello', x: 10, y: 20, schemaVersion: 1 });

    commands.undo();
    expect(readBlocks(doc)[0]).toMatchObject({ text: '' });
    commands.redo();
    expect(readBlocks(doc)[0]).toMatchObject({ text: 'hello' });
  });

  it('does not undo transactions from another collaboration origin', () => {
    const doc = new Y.Doc();
    const commands = new BoardCommands(doc, () => true);
    const id = commands.createText(0, 0);
    commands.stopCapturing();
    doc.transact(() => blocksMap(doc).get(id)?.set('x', 50), { kind: 'remote-provider' });
    commands.move([id], 5, 0);
    expect(readBlocks(doc)[0].x).toBe(55);
    commands.undo();
    expect(readBlocks(doc)[0].x).toBe(50);
  });

  it('rejects viewer mutations and preserves image references on clipboard paste', () => {
    const doc = new Y.Doc();
    const viewer = new BoardCommands(doc, () => false);
    expect(viewer.createText(0, 0)).toBe('');
    expect(readBlocks(doc)).toHaveLength(0);

    const editor = new BoardCommands(doc, () => true);
    const id = editor.createImage({ x: 1, y: 2, assetId: 'ast_1', alt: 'photo', naturalWidth: 800, naturalHeight: 600, width: 400, height: 300 });
    const serialized = exportBlocks(readBlocks(doc));
    const pasted = editor.paste(importBlocks(serialized));
    const blocks = readBlocks(doc);
    expect(pasted).toHaveLength(1);
    expect(pasted[0]).not.toBe(id);
    expect(blocks.every((block) => block.type !== 'image' || block.assetId === 'ast_1')).toBe(true);
  });

  it('aligns and distributes a multi-selection in one command layer', () => {
    const doc = new Y.Doc();
    const commands = new BoardCommands(doc, () => true);
    const ids = [commands.createText(0, 0), commands.createText(300, 80), commands.createText(700, 160)];
    commands.align(ids, 'horizontal');
    const aligned = readBlocks(doc);
    expect(new Set(aligned.map((block) => block.y + block.height / 2)).size).toBe(1);
    commands.distribute(ids, 'horizontal');
    const distributed = readBlocks(doc).sort((a, b) => a.x - b.x);
    const gapA = distributed[1].x - (distributed[0].x + distributed[0].width);
    const gapB = distributed[2].x - (distributed[1].x + distributed[1].width);
    expect(gapA).toBeCloseTo(gapB);
  });
});

describe('Yjs convergence', () => {
  it('converges concurrent text updates from two clients', () => {
    const first = new Y.Doc();
    const firstCommands = new BoardCommands(first, () => true);
    const id = firstCommands.createText(0, 0);
    firstCommands.replaceText(id, 'seed');
    const second = new Y.Doc();
    Y.applyUpdate(second, Y.encodeStateAsUpdate(first));
    const secondCommands = new BoardCommands(second, () => true);

    const firstState = Y.encodeStateVector(first);
    const secondState = Y.encodeStateVector(second);
    firstCommands.replaceText(id, 'alpha');
    secondCommands.replaceText(id, 'beta');
    const firstUpdate = Y.encodeStateAsUpdate(first, firstState);
    const secondUpdate = Y.encodeStateAsUpdate(second, secondState);
    Y.applyUpdate(first, secondUpdate, { kind: 'remote' });
    Y.applyUpdate(second, firstUpdate, { kind: 'remote' });

    expect(Y.encodeStateAsUpdate(first)).toEqual(Y.encodeStateAsUpdate(second));
    const firstBlock = readBlocks(first)[0];
    const secondBlock = readBlocks(second)[0];
    expect(firstBlock.type).toBe('text');
    expect(secondBlock.type).toBe('text');
    if (firstBlock.type === 'text' && secondBlock.type === 'text') expect(firstBlock.text).toBe(secondBlock.text);
  });
});
