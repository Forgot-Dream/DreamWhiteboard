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
  });
});
