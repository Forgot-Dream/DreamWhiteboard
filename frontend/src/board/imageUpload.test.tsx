import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { uploadAsset, type Asset } from '../lib/api';
import { I18nProvider } from '../lib/i18n';
import type { BoardRuntime } from './runtime';
import { useBoardImageUpload } from './imageUpload';
import { useBoardStore } from './store';

vi.mock('../lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../lib/api')>();
  return { ...original, uploadAsset: vi.fn() };
});

describe('board image uploads', () => {
  beforeEach(() => {
    vi.mocked(uploadAsset).mockReset();
    useBoardStore.getState().reset('board-1');
    useBoardStore.getState().setViewport({ x: 100, y: 50, scale: 2 });
    vi.stubGlobal('Image', LoadableImage);
    vi.stubGlobal('URL', { createObjectURL: vi.fn(() => 'blob:image'), revokeObjectURL: vi.fn() });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('uploads the file and creates a fitted image block at the viewport center', async () => {
    const createImage = vi.fn(() => 'block-image');
    const runtime = runtimeStub(createImage);
    const asset: Asset = {
      id: 'asset-1', project_id: runtime.projectID, file_name: 'capture.png', content_type: 'image/png',
      size: 3, width: 1200, height: 600, created_at: '2026-01-01T00:00:00Z'
    };
    vi.mocked(uploadAsset).mockResolvedValue(asset);
    const { result } = renderHook(() => useBoardImageUpload(runtime), { wrapper: I18nProvider });
    const file = new File(['png'], 'capture.png', { type: 'image/png' });

    await act(() => result.current.upload(file));

    expect(uploadAsset).toHaveBeenCalledWith(runtime.projectID, file, expect.objectContaining({ signal: expect.any(AbortSignal), onProgress: expect.any(Function) }));
    const center = {
      x: (window.innerWidth / 2 - 100) / 2,
      y: ((window.innerHeight - 58) / 2 - 50) / 2
    };
    expect(createImage).toHaveBeenCalledWith({
      x: center.x - 280,
      y: center.y - 140,
      assetId: asset.id,
      alt: file.name,
      naturalWidth: 1200,
      naturalHeight: 600,
      width: 560,
      height: 280
    });
    expect(useBoardStore.getState().selection.ids).toEqual(['block-image']);
    expect(result.current.status).toBeNull();
  });

  it('reuses localized validation and upload error feedback', async () => {
    const runtime = runtimeStub(vi.fn());
    const { result } = renderHook(() => useBoardImageUpload(runtime), { wrapper: I18nProvider });

    await act(() => result.current.upload(new File(['text'], 'notes.txt', { type: 'text/plain' })));
    expect(result.current.status?.error).toBe('Choose a PNG, JPEG, or GIF image.');
    expect(uploadAsset).not.toHaveBeenCalled();

    vi.mocked(uploadAsset).mockRejectedValueOnce(new Error('Upload failed'));
    await act(() => result.current.upload(new File(['png'], 'capture.png', { type: 'image/png' })));
    expect(result.current.status?.error).toBe('Could not upload the file.');
  });

  it('does not start the request when cancellation happens during image decoding', async () => {
    vi.stubGlobal('Image', PendingImage);
    const runtime = runtimeStub(vi.fn());
    const { result } = renderHook(() => useBoardImageUpload(runtime), { wrapper: I18nProvider });
    let completion: Promise<void> | undefined;

    act(() => {
      completion = result.current.upload(new File(['png'], 'capture.png', { type: 'image/png' }));
    });
    expect(result.current.upload(new File(['png'], 'second.png', { type: 'image/png' }))).toBeUndefined();
    act(() => result.current.cancel());
    await act(async () => { await completion; });

    expect(uploadAsset).not.toHaveBeenCalled();
    expect(result.current.status).toBeNull();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:image');
  });

  it('aborts an in-flight upload when the board runtime changes', async () => {
    vi.mocked(uploadAsset).mockImplementation((_projectID, _file, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new DOMException('Upload cancelled', 'AbortError')), { once: true });
    }));
    const firstCreate = vi.fn();
    const first = runtimeStub(firstCreate);
    const second = runtimeStub(vi.fn());
    const { result, rerender } = renderHook(
      ({ runtime }) => useBoardImageUpload(runtime),
      { initialProps: { runtime: first }, wrapper: I18nProvider }
    );
    let completion: Promise<void> | undefined;

    await act(async () => {
      completion = result.current.upload(new File(['png'], 'capture.png', { type: 'image/png' }));
      await Promise.resolve();
    });
    rerender({ runtime: second });
    await act(async () => { await completion; });

    expect(firstCreate).not.toHaveBeenCalled();
    expect(result.current.status).toBeNull();
  });

  it('reports when an uploaded asset cannot be inserted into the active document', async () => {
    const runtime = runtimeStub(vi.fn(() => ''));
    vi.mocked(uploadAsset).mockResolvedValue({
      id: 'orphaned-asset', project_id: runtime.projectID, file_name: 'capture.png', content_type: 'image/png',
      size: 3, width: 10, height: 10, created_at: '2026-01-01T00:00:00Z'
    });
    const { result } = renderHook(() => useBoardImageUpload(runtime), { wrapper: I18nProvider });

    await act(() => result.current.upload(new File(['png'], 'capture.png', { type: 'image/png' })));

    expect(result.current.status?.error).toBe('The image was uploaded but could not be added to this board.');
    expect(useBoardStore.getState().selection.ids).toEqual([]);
  });
});

function runtimeStub(createImage: ReturnType<typeof vi.fn>) {
  return {
    canEdit: true,
    projectID: 'project-1',
    commands: { createImage }
  } as unknown as BoardRuntime;
}

class LoadableImage {
  naturalWidth = 1200;
  naturalHeight = 600;
  onload: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  set src(_value: string) {
    queueMicrotask(() => this.onload?.(new Event('load')));
  }
}

class PendingImage {
  static latest: PendingImage | null = null;
  naturalWidth = 1200;
  naturalHeight = 600;
  onload: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor() {
    PendingImage.latest = this;
  }

  set src(_value: string) { /* wait for the test to finish decoding */ }
}
