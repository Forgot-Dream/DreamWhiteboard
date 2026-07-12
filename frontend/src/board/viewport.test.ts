import { act, fireEvent, render } from '@testing-library/react';
import { createElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../lib/i18n';
import { CanvasViewport, filterVisibleBlocks } from './CanvasViewport';
import type { BoardRuntime } from './runtime';
import type { ImageBlock, TextBlock } from './schema';
import { useBoardStore } from './store';

const originalSetPointerCapture = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'setPointerCapture');

afterEach(() => {
  vi.unstubAllGlobals();
  if (originalSetPointerCapture) Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', originalSetPointerCapture);
  else delete (HTMLElement.prototype as Partial<HTMLElement>).setPointerCapture;
});

describe('viewport spatial filtering', () => {
  it('keeps most of a 1,000-block document out of the React render set', () => {
    const blocks: TextBlock[] = Array.from({ length: 1_000 }, (_, index) => ({
      schemaVersion: 1,
      id: `block-${index}`,
      type: 'text',
      text: String(index),
      x: (index % 50) * 500,
      y: Math.floor(index / 50) * 400,
      width: 180,
      height: 80,
      z: index,
      style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
    }));
    const started = performance.now();
    const visible = filterVisibleBlocks(blocks, { x: 0, y: 0, scale: 1 }, { width: 1_280, height: 720 });
    const elapsed = performance.now() - started;

    expect(visible.length).toBeGreaterThan(0);
    expect(visible.length).toBeLessThan(40);
    expect(elapsed).toBeLessThan(100);
  });

  it('always keeps an offscreen selected block mounted', () => {
    const block: TextBlock = {
      schemaVersion: 1, id: 'selected', type: 'text', text: '', x: 50_000, y: 50_000,
      width: 100, height: 50, z: 1,
      style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
    };
    expect(filterVisibleBlocks([block], { x: 0, y: 0, scale: 1 }, { width: 800, height: 600 }, new Set(['selected']))).toEqual([block]);
  });

  it('pans blank canvas with the merged select tool without creating document operations', () => {
    const block = imageBlock('selected', 500, 500);
    const move = vi.fn();
    const runtime = canvasRuntime(false, move);
    const view = renderCanvas(runtime, [block], 'pan-board');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    expect(canvas).not.toBeNull();
    act(() => {
      useBoardStore.setState({ viewport: { x: 20, y: 30, scale: 1 } });
      useBoardStore.getState().setSelection([block.id]);
    });
    const revision = useBoardStore.getState().document.revision;
    const sequence = useBoardStore.getState().connection.lastSequence;

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 100, clientY: 120 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 150, clientY: 150 }));
    act(() => view.flushFrames());

    expect(useBoardStore.getState().viewport).toEqual({ x: 70, y: 60, scale: 1 });
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 150, clientY: 150 }));

    expect(useBoardStore.getState().viewport).toEqual({ x: 70, y: 60, scale: 1 });
    expect(useBoardStore.getState().selection.ids).toEqual([block.id]);
    expect(useBoardStore.getState().document.revision).toBe(revision);
    expect(useBoardStore.getState().connection.lastSequence).toBe(sequence);
    expect(move).not.toHaveBeenCalled();
  });

  it('clears selection on a blank click without nudging the viewport', () => {
    const block = imageBlock('selected', 500, 500);
    const runtime = canvasRuntime(true, vi.fn());
    const view = renderCanvas(runtime, [block], 'blank-click-board');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => {
      useBoardStore.setState({ viewport: { x: 25, y: 35, scale: 1 } });
      useBoardStore.getState().setSelection([block.id]);
    });

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 80, clientY: 90 }));
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 80, clientY: 90 }));

    expect(useBoardStore.getState().selection.ids).toEqual([]);
    expect(useBoardStore.getState().viewport).toEqual({ x: 25, y: 35, scale: 1 });
  });

  it('treats sub-threshold pointer jitter as a blank click', () => {
    const block = imageBlock('selected', 500, 500);
    const runtime = canvasRuntime(true, vi.fn());
    const view = renderCanvas(runtime, [block], 'pointer-jitter-board');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => {
      useBoardStore.setState({ viewport: { x: 15, y: 25, scale: 1 } });
      useBoardStore.getState().setSelection([block.id]);
    });

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 80, clientY: 90 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 82, clientY: 92 }));
    act(() => view.flushFrames());
    expect(useBoardStore.getState().viewport).toEqual({ x: 15, y: 25, scale: 1 });
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 82, clientY: 92 }));

    expect(useBoardStore.getState().selection.ids).toEqual([]);
    expect(useBoardStore.getState().viewport).toEqual({ x: 15, y: 25, scale: 1 });
  });

  it('keeps Shift-drag marquee selection while ordinary blank drag pans', () => {
    const inside = imageBlock('inside', 100, 100);
    const existing = imageBlock('existing', 500, 500);
    const move = vi.fn();
    const runtime = canvasRuntime(true, move);
    const view = renderCanvas(runtime, [inside, existing], 'marquee-board');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => {
      useBoardStore.setState({ viewport: { x: 0, y: 0, scale: 1 } });
      useBoardStore.getState().setSelection([existing.id]);
    });

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 80, clientY: 80, shiftKey: true }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 320, clientY: 260, shiftKey: true }));
    act(() => view.flushFrames());
    expect(useBoardStore.getState().selection.box).toEqual({ x: 80, y: 80, width: 240, height: 180 });
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 320, clientY: 260, shiftKey: true }));

    expect(useBoardStore.getState().selection.ids).toEqual([existing.id, inside.id]);
    expect(useBoardStore.getState().viewport).toEqual({ x: 0, y: 0, scale: 1 });
    expect(move).not.toHaveBeenCalled();
  });

  it('pans from a block with the middle button without changing selection', () => {
    const target = imageBlock('target', 100, 100);
    const selected = imageBlock('selected', 500, 100);
    const move = vi.fn();
    const runtime = canvasRuntime(true, move);
    const view = renderCanvas(runtime, [target, selected], 'middle-pan-board');
    const targetElement = view.container.querySelector<HTMLElement>('.block-image');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => {
      useBoardStore.setState({ viewport: { x: 0, y: 0, scale: 1 } });
      useBoardStore.getState().setSelection([selected.id]);
    });

    fireEvent(targetElement!, new MouseEvent('pointerdown', { bubbles: true, button: 1, clientX: 120, clientY: 120 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 160, clientY: 145 }));
    act(() => view.flushFrames());
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 160, clientY: 145 }));

    expect(useBoardStore.getState().viewport).toEqual({ x: 40, y: 25, scale: 1 });
    expect(useBoardStore.getState().selection.ids).toEqual([selected.id]);
    expect(move).not.toHaveBeenCalled();
  });

  it('lets viewers pan from a block while keeping the block selectable', () => {
    const block = imageBlock('viewer-block', 100, 100);
    const move = vi.fn();
    const runtime = canvasRuntime(false, move);
    const view = renderCanvas(runtime, [block], 'viewer-block-pan-board');
    const blockElement = view.container.querySelector<HTMLElement>('.block-image');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => useBoardStore.setState({ viewport: { x: 0, y: 0, scale: 1 } }));

    fireEvent(blockElement!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 120, clientY: 120 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 150, clientY: 140 }));
    act(() => view.flushFrames());
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 150, clientY: 140 }));

    expect(useBoardStore.getState().viewport).toEqual({ x: 30, y: 20, scale: 1 });
    expect(useBoardStore.getState().selection.ids).toEqual([block.id]);
    expect(move).not.toHaveBeenCalled();
  });

  it.each([
    { label: 'viewer left drag', canEdit: false, button: 0 },
    { label: 'editor middle drag', canEdit: true, button: 1 }
  ])('pans from text content with $label', ({ label, canEdit, button }) => {
    const block = textBlock(`text-${label.replace(/ /g, '-')}`, 100, 100);
    const move = vi.fn();
    const runtime = canvasRuntime(canEdit, move);
    const view = renderCanvas(runtime, [block], `text-pan-${button}-${String(canEdit)}`);
    const textarea = view.getByRole('textbox');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => useBoardStore.setState({ viewport: { x: 0, y: 0, scale: 1 } }));

    fireEvent(textarea, new MouseEvent('pointerdown', { bubbles: true, button, clientX: 120, clientY: 120 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 155, clientY: 140 }));
    act(() => view.flushFrames());
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 155, clientY: 140 }));

    expect(useBoardStore.getState().viewport).toEqual({ x: 35, y: 20, scale: 1 });
    expect(useBoardStore.getState().selection.ids).toEqual(canEdit ? [] : [block.id]);
    expect(move).not.toHaveBeenCalled();
  });

  it('cancels blank gestures without clearing or committing selection', () => {
    const inside = imageBlock('inside', 100, 100);
    const selected = imageBlock('selected', 500, 500);
    const runtime = canvasRuntime(true, vi.fn());
    const view = renderCanvas(runtime, [inside, selected], 'cancel-board');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    act(() => {
      useBoardStore.setState({ viewport: { x: 0, y: 0, scale: 1 } });
      useBoardStore.getState().setSelection([selected.id]);
    });

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 60, clientY: 60 }));
    fireEvent(canvas!, new MouseEvent('pointercancel', { bubbles: true, button: 0, clientX: 60, clientY: 60 }));
    expect(useBoardStore.getState().selection.ids).toEqual([selected.id]);

    fireEvent(canvas!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 80, clientY: 80, shiftKey: true }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 320, clientY: 260, shiftKey: true }));
    act(() => view.flushFrames());
    expect(useBoardStore.getState().selection.box).not.toBeNull();
    fireEvent(canvas!, new MouseEvent('pointercancel', { bubbles: true, button: 0, clientX: 320, clientY: 260, shiftKey: true }));

    expect(useBoardStore.getState().selection.ids).toEqual([selected.id]);
    expect(useBoardStore.getState().selection.box).toBeNull();
    expect(useBoardStore.getState().interaction).toEqual({ kind: 'idle', active: false });
  });

  it('starts a memoized block drag in the latest viewport coordinates', () => {
    const block: ImageBlock = {
      schemaVersion: 1, id: 'image', type: 'image', assetId: 'asset', alt: 'image',
      x: 100, y: 100, width: 200, height: 120, z: 1,
      naturalWidth: 200, naturalHeight: 120, aspectLocked: true,
      style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
    };
    const move = vi.fn();
    const runtime = {
      canEdit: true,
      commands: { move, stopCapturing: vi.fn() },
      provider: { updateLocalPresence: vi.fn() }
    } as unknown as BoardRuntime;
    vi.stubGlobal('ResizeObserver', class {
      observe() { /* no-op */ }
      disconnect() { /* no-op */ }
    });
    vi.stubGlobal('requestAnimationFrame', vi.fn(() => 1));
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', { configurable: true, value: vi.fn() });

    useBoardStore.getState().reset('board');
    useBoardStore.getState().setBlocks([block]);
    useBoardStore.getState().setSelection([block.id]);
    const view = render(createElement(I18nProvider, null, createElement(CanvasViewport, { runtime })));
    const renderedBlock = view.container.querySelector<HTMLElement>('.block-image');
    const canvas = view.container.querySelector<HTMLElement>('.canvas');
    expect(renderedBlock).not.toBeNull();
    expect(canvas).not.toBeNull();

    act(() => useBoardStore.setState({ viewport: { x: 100, y: 60, scale: 2 } }));
    fireEvent(renderedBlock!, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 300, clientY: 260 }));
    fireEvent(canvas!, new MouseEvent('pointermove', { bubbles: true, clientX: 320, clientY: 260 }));
    fireEvent(canvas!, new MouseEvent('pointerup', { bubbles: true, clientX: 320, clientY: 260 }));

    expect(move).toHaveBeenCalledWith(['image'], 10, 0);
    expect(useBoardStore.getState().viewport).toEqual({ x: 100, y: 60, scale: 2 });
  });
});

function imageBlock(id: string, x: number, y: number): ImageBlock {
  return {
    schemaVersion: 1, id, type: 'image', assetId: `asset-${id}`, alt: id,
    x, y, width: 200, height: 120, z: 1,
    naturalWidth: 200, naturalHeight: 120, aspectLocked: true,
    style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
  };
}

function textBlock(id: string, x: number, y: number): TextBlock {
  return {
    schemaVersion: 1, id, type: 'text', text: id,
    x, y, width: 200, height: 80, z: 1,
    style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
  };
}

function canvasRuntime(canEdit: boolean, move: ReturnType<typeof vi.fn>) {
  return {
    canEdit,
    commands: { move, stopCapturing: vi.fn() },
    provider: { updateLocalPresence: vi.fn() }
  } as unknown as BoardRuntime;
}

function renderCanvas(runtime: BoardRuntime, blocks: Array<ImageBlock | TextBlock>, boardID: string) {
  vi.stubGlobal('ResizeObserver', class {
    observe() { /* no-op */ }
    disconnect() { /* no-op */ }
  });
  let nextFrameID = 0;
  const frames = new Map<number, FrameRequestCallback>();
  vi.stubGlobal('requestAnimationFrame', vi.fn((callback: FrameRequestCallback) => {
    nextFrameID += 1;
    frames.set(nextFrameID, callback);
    return nextFrameID;
  }));
  vi.stubGlobal('cancelAnimationFrame', vi.fn((frameID: number) => { frames.delete(frameID); }));
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', { configurable: true, value: vi.fn() });
  useBoardStore.getState().reset(boardID);
  useBoardStore.getState().setBlocks(blocks);
  return {
    ...render(createElement(I18nProvider, null, createElement(CanvasViewport, { runtime }))),
    flushFrames() {
      const pending = Array.from(frames.values());
      frames.clear();
      pending.forEach((callback) => callback(performance.now()));
    }
  };
}
