import type { BlockType, WhiteboardBlock } from './api';

export interface BlockDefinition {
  type: BlockType;
  label: string;
  defaults: Pick<WhiteboardBlock, 'w' | 'h' | 'data'>;
  schema: Record<string, unknown>;
}

export const blockRegistry: Record<BlockType, BlockDefinition> = {
  rich_text: {
    type: 'rich_text',
    label: 'Rich text',
    defaults: { w: 360, h: 220, data: { doc: { type: 'doc', content: [{ type: 'paragraph' }] } } },
    schema: { type: 'object', properties: { doc: { type: 'object' } }, required: ['doc'] }
  },
  note: {
    type: 'note',
    label: 'Note',
    defaults: { w: 260, h: 160, data: { text: 'New note' } },
    schema: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] }
  },
  image: {
    type: 'image',
    label: 'Image',
    defaults: { w: 320, h: 220, data: { asset_id: '', url: '', alt: '' } },
    schema: { type: 'object', properties: { asset_id: { type: 'string' }, url: { type: 'string' }, alt: { type: 'string' } } }
  },
  shape: {
    type: 'shape',
    label: 'Shape',
    defaults: { w: 180, h: 120, data: { color: '#2f6f73', shape: 'rectangle' } },
    schema: { type: 'object', properties: { color: { type: 'string' }, shape: { enum: ['rectangle', 'ellipse'] } } }
  }
};

export function createBlock(type: BlockType, x: number, y: number, z: number): WhiteboardBlock {
  const definition = blockRegistry[type];
  return {
    id: `blk_${crypto.randomUUID()}`,
    type,
    x,
    y,
    w: definition.defaults.w,
    h: definition.defaults.h,
    z,
    data: structuredClone(definition.defaults.data)
  };
}

