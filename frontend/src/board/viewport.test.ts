import { describe, expect, it } from 'vitest';
import { filterVisibleBlocks } from './CanvasViewport';
import type { TextBlock } from './schema';

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
});
