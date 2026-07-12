import { useCallback, useEffect, useRef, useState } from 'react';
import { uploadAsset } from '../lib/api';
import { useI18n } from '../lib/i18n';
import type { BoardRuntime } from './runtime';
import { useBoardStore, type Viewport } from './store';

const MAX_CLIENT_UPLOAD = 25 * 1024 * 1024;
const ALLOWED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif']);

export interface ImageUploadStatus {
  file: File;
  progress: number;
  error: string;
}

export interface BoardImageUpload {
  status: ImageUploadStatus | null;
  upload: (file: File) => Promise<void> | undefined;
  cancel: () => void;
  retry: () => void;
}

export function useBoardImageUpload(runtime: BoardRuntime): BoardImageUpload {
  const { errorMessage, t } = useI18n();
  const [status, setStatus] = useState<ImageUploadStatus | null>(null);
  const activeController = useRef<AbortController | null>(null);

  const upload = useCallback((file: File) => {
    if (!runtime.canEdit || activeController.current) return;
    if (!ALLOWED_IMAGE_TYPES.has(file.type)) {
      setStatus({ file, progress: 0, error: t('editor.upload.invalidType') });
      return Promise.resolve();
    }
    if (file.size > MAX_CLIENT_UPLOAD) {
      setStatus({ file, progress: 0, error: t('editor.upload.tooLarge') });
      return Promise.resolve();
    }

    const controller = new AbortController();
    activeController.current = controller;
    setStatus({ file, progress: 0, error: '' });
    return (async () => {
      try {
        const dimensions = await readImageDimensions(file, t('editor.upload.decodeFailed'), controller.signal);
        const asset = await uploadAsset(runtime.projectID, file, {
          signal: controller.signal,
          onProgress: (progress) => {
            if (activeController.current === controller) {
              setStatus((current) => current ? { ...current, progress } : null);
            }
          }
        });
        if (controller.signal.aborted || activeController.current !== controller) {
          throw new DOMException('Upload cancelled', 'AbortError');
        }
        const naturalWidth = asset.width ?? dimensions.width;
        const naturalHeight = asset.height ?? dimensions.height;
        const size = fitImageSize(naturalWidth, naturalHeight);
        const viewport = useBoardStore.getState().viewport;
        const center = screenToWorld(window.innerWidth / 2, (window.innerHeight - 58) / 2, viewport);
        const id = runtime.commands.createImage({
          x: center.x - size.width / 2,
          y: center.y - size.height / 2,
          assetId: asset.id,
          alt: file.name,
          naturalWidth,
          naturalHeight,
          width: size.width,
          height: size.height
        });
        if (!id) {
          setStatus((current) => current ? { ...current, error: t('editor.upload.insertFailed') } : null);
          return;
        }
        useBoardStore.getState().setSelection([id]);
        setStatus(null);
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') {
          setStatus(null);
          return;
        }
        setStatus((current) => current ? { ...current, error: errorMessage(error, 'editor.upload.failed') } : null);
      } finally {
        if (activeController.current === controller) activeController.current = null;
      }
    })();
  }, [errorMessage, runtime, t]);

  const cancel = useCallback(() => activeController.current?.abort(), []);
  const retry = useCallback(() => {
    if (status) void upload(status.file);
  }, [status, upload]);

  useEffect(() => () => activeController.current?.abort(), [runtime]);

  return { status, upload, cancel, retry };
}

function screenToWorld(x: number, y: number, viewport: Viewport) {
  return { x: (x - viewport.x) / viewport.scale, y: (y - viewport.y) / viewport.scale };
}

function readImageDimensions(file: File, decodeError: string, signal: AbortSignal) {
  return new Promise<{ width: number; height: number }>((resolve, reject) => {
    const image = new Image();
    const url = URL.createObjectURL(file);
    const cleanup = () => {
      signal.removeEventListener('abort', abort);
      URL.revokeObjectURL(url);
      image.onload = null;
      image.onerror = null;
    };
    const abort = () => {
      cleanup();
      image.src = '';
      reject(new DOMException('Upload cancelled', 'AbortError'));
    };
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    image.onload = () => {
      cleanup();
      resolve({ width: image.naturalWidth || 320, height: image.naturalHeight || 220 });
    };
    image.onerror = () => {
      cleanup();
      reject(new Error(decodeError));
    };
    image.src = url;
  });
}

function fitImageSize(width: number, height: number) {
  const ratio = Math.min(1, 560 / Math.max(width, 1), 400 / Math.max(height, 1));
  return { width: Math.max(80, Math.round(width * ratio)), height: Math.max(54, Math.round(height * ratio)) };
}
