import * as Y from 'yjs';
import { assetURL } from '../lib/api';

export const BOARD_SCHEMA_VERSION = 1 as const;

export interface BlockStyle {
  fill: string;
  textColor: string;
  borderColor: string;
  borderWidth: number;
}

interface BaseBlock {
  schemaVersion: typeof BOARD_SCHEMA_VERSION;
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  z: number;
  style: BlockStyle;
}

export interface TextBlock extends BaseBlock {
  type: 'text';
  text: string;
}

export interface ImageBlock extends BaseBlock {
  type: 'image';
  assetId: string;
  alt: string;
  naturalWidth: number;
  naturalHeight: number;
  aspectLocked: boolean;
}

export type WhiteboardBlock = TextBlock | ImageBlock;

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export const defaultStyle: BlockStyle = {
  fill: 'transparent',
  textColor: '#253531',
  borderColor: '#91a29d',
  borderWidth: 0
};

export function createTextBlock(x: number, y: number, z: number, text = ''): TextBlock {
  return {
    schemaVersion: BOARD_SCHEMA_VERSION,
    id: blockID(), type: 'text', x, y, width: 200, height: 64, z, text,
    style: { ...defaultStyle }
  };
}

export function createImageBlock(input: {
  x: number; y: number; z: number; assetId: string; alt: string;
  naturalWidth: number; naturalHeight: number; width: number; height: number;
}): ImageBlock {
  return {
    schemaVersion: BOARD_SCHEMA_VERSION,
    id: blockID(), type: 'image', x: input.x, y: input.y, width: input.width, height: input.height, z: input.z,
    assetId: input.assetId, alt: input.alt, naturalWidth: input.naturalWidth, naturalHeight: input.naturalHeight,
    aspectLocked: true, style: { ...defaultStyle }
  };
}

export function blocksMap(doc: Y.Doc) {
  return doc.getMap<Y.Map<unknown>>('blocks');
}

export function blockToYMap(block: WhiteboardBlock) {
  const map = new Y.Map<unknown>();
  map.set('schema_version', block.schemaVersion);
  map.set('type', block.type);
  map.set('x', block.x);
  map.set('y', block.y);
  map.set('width', block.width);
  map.set('height', block.height);
  map.set('z', block.z);
  const style = new Y.Map<unknown>();
  style.set('fill', block.style.fill);
  style.set('text_color', block.style.textColor);
  style.set('border_color', block.style.borderColor);
  style.set('border_width', block.style.borderWidth);
  map.set('style', style);
  if (block.type === 'text') {
    const text = new Y.Text();
    text.insert(0, block.text);
    map.set('text', text);
  } else {
    const image = new Y.Map<unknown>();
    image.set('asset_id', block.assetId);
    image.set('alt', block.alt);
    image.set('natural_width', block.naturalWidth);
    image.set('natural_height', block.naturalHeight);
    image.set('aspect_locked', block.aspectLocked);
    map.set('image', image);
  }
  return map;
}

export function yMapToBlock(id: string, map: Y.Map<unknown>): WhiteboardBlock | null {
  const type = map.get('type');
  if (type !== 'text' && type !== 'image') return null;
  const styleMap = map.get('style');
  const style: BlockStyle = styleMap instanceof Y.Map ? {
    fill: stringValue(styleMap.get('fill'), defaultStyle.fill),
    textColor: stringValue(styleMap.get('text_color'), defaultStyle.textColor),
    borderColor: stringValue(styleMap.get('border_color'), defaultStyle.borderColor),
    borderWidth: numberValue(styleMap.get('border_width'), defaultStyle.borderWidth)
  } : { ...defaultStyle };
  const base: BaseBlock = {
    schemaVersion: BOARD_SCHEMA_VERSION,
    id,
    x: numberValue(map.get('x'), 0),
    y: numberValue(map.get('y'), 0),
    width: Math.max(24, numberValue(map.get('width'), 200)),
    height: Math.max(24, numberValue(map.get('height'), 64)),
    z: numberValue(map.get('z'), 0),
    style
  };
  if (type === 'text') {
    const text = map.get('text');
    return { ...base, type, text: text instanceof Y.Text ? text.toString() : stringValue(text, '') };
  }
  const image = map.get('image');
  return {
    ...base,
    type,
    assetId: image instanceof Y.Map ? stringValue(image.get('asset_id'), '') : '',
    alt: image instanceof Y.Map ? stringValue(image.get('alt'), '') : '',
    naturalWidth: image instanceof Y.Map ? numberValue(image.get('natural_width'), base.width) : base.width,
    naturalHeight: image instanceof Y.Map ? numberValue(image.get('natural_height'), base.height) : base.height,
    aspectLocked: image instanceof Y.Map ? image.get('aspect_locked') !== false : true
  };
}

export function readBlocks(doc: Y.Doc) {
  const result: WhiteboardBlock[] = [];
  blocksMap(doc).forEach((value, id) => {
    const block = yMapToBlock(id, value);
    if (block) result.push(block);
  });
  return result.sort((a, b) => a.z - b.z || a.id.localeCompare(b.id));
}

export function referencedAssetsByBlock(doc: Y.Doc) {
  const references = new Map<string, string>();
  blocksMap(doc).forEach((value, blockID) => {
    if (!(value instanceof Y.Map) || value.get('type') !== 'image') return;
    const image = value.get('image');
    const assetID = image instanceof Y.Map ? image.get('asset_id') : undefined;
    if (typeof assetID === 'string' && assetID) references.set(blockID, assetID);
  });
  return references;
}

export function introducedAssetIDs(previous: ReadonlyMap<string, string>, current: ReadonlyMap<string, string>) {
  const introduced = new Set<string>();
  current.forEach((assetID, blockID) => {
    if (previous.get(blockID) !== assetID) introduced.add(assetID);
  });
  return Array.from(introduced).sort();
}

export function referencedAssetIDs(doc: Y.Doc) {
  return Array.from(new Set(referencedAssetsByBlock(doc).values())).sort();
}

export function blockBounds(blocks: WhiteboardBlock[]): Rect | null {
  if (blocks.length === 0) return null;
  const left = Math.min(...blocks.map((block) => block.x));
  const top = Math.min(...blocks.map((block) => block.y));
  const right = Math.max(...blocks.map((block) => block.x + block.width));
  const bottom = Math.max(...blocks.map((block) => block.y + block.height));
  return { x: left, y: top, width: right - left, height: bottom - top };
}

export function intersects(a: Rect, b: Rect) {
  return a.x <= b.x + b.width && a.x + a.width >= b.x && a.y <= b.y + b.height && a.y + a.height >= b.y;
}

export function imageSource(block: ImageBlock) {
  return assetURL(block.assetId);
}

function blockID() {
  return `blk_${crypto.randomUUID()}`;
}

function stringValue(value: unknown, fallback: string) {
  return typeof value === 'string' ? value : fallback;
}

function numberValue(value: unknown, fallback: number) {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}
