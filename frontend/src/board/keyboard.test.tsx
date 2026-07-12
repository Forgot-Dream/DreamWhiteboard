import { render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { BoardRuntime } from './runtime';
import type { TextBlock } from './schema';
import { exportBlocks, importBlocks } from './commands';
import { useBoardKeyboard } from './keyboard';
import { useBoardStore } from './store';

describe('whiteboard clipboard handling', () => {
  beforeEach(() => useBoardStore.getState().reset('board-1'));

  it('uploads an image blob from the system clipboard before considering text', () => {
    const pasteImage = vi.fn((_file: File) => true);
    const runtime = runtimeStub(true);
    render(<KeyboardHarness runtime={runtime} pasteImage={pasteImage} />);
    const blob = new Blob(['png'], { type: 'image/png' });
    const second = new File(['gif'], 'second.gif', { type: 'image/gif' });
    const event = pasteEvent({
      items: [
        { kind: 'file', type: 'image/png', getAsFile: () => blob as File },
        { kind: 'file', type: 'image/gif', getAsFile: () => second }
      ],
      text: '{"type":"dreamwhiteboard/blocks"}'
    });

    window.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
    expect(pasteImage).toHaveBeenCalledTimes(1);
    expect(pasteImage.mock.calls[0][0]).toBeInstanceOf(File);
    expect(pasteImage.mock.calls[0][0]).toMatchObject({ name: 'clipboard-image.png', type: 'image/png' });
    expect(runtime.commands.paste).not.toHaveBeenCalled();
  });

  it('does not swallow another image paste while the uploader is busy', () => {
    const pasteImage = vi.fn((_file: File) => false);
    const runtime = runtimeStub(true);
    render(<KeyboardHarness runtime={runtime} pasteImage={pasteImage} />);
    const event = pasteEvent({ files: [new File(['png'], 'second.png', { type: 'image/png' })] });

    window.dispatchEvent(event);

    expect(pasteImage).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps internal block paste working through the paste event', () => {
    const runtime = runtimeStub(true);
    vi.mocked(runtime.commands.paste).mockReturnValue(['pasted-block']);
    render(<KeyboardHarness runtime={runtime} pasteImage={vi.fn(() => true)} />);
    const block: TextBlock = {
      schemaVersion: 1,
      id: 'source',
      type: 'text',
      text: 'hello',
      x: 10,
      y: 20,
      width: 200,
      height: 80,
      z: 1,
      style: { fill: 'transparent', textColor: '#111111', borderColor: '#999999', borderWidth: 0 }
    };
    const event = pasteEvent({ text: exportBlocks([block], runtime.projectID) });

    window.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
    expect(runtime.commands.paste).toHaveBeenCalledWith([block]);
    expect(useBoardStore.getState().selection.ids).toEqual(['pasted-block']);
  });

  it('copies through the synchronous clipboard event and never substitutes stale external text', () => {
    const runtime = runtimeStub(true);
    vi.mocked(runtime.commands.paste).mockReturnValue(['copied-block']);
    render(<KeyboardHarness runtime={runtime} pasteImage={vi.fn(() => true)} />);
    const block = textBlock();
    useBoardStore.getState().setBlocks([block]);
    useBoardStore.getState().setSelection([block.id]);
    let copied = '';
    const copy = clipboardEvent('copy', {
      getData: () => '',
      setData: (type, value) => { if (type === 'text/plain') copied = value; }
    });

    window.dispatchEvent(copy);

    expect(copy.defaultPrevented).toBe(true);
    expect(importBlocks(copied, runtime.projectID)).toEqual([block]);

    const stalePaste = pasteEvent({ text: 'external clipboard text' });
    window.dispatchEvent(stalePaste);
    expect(stalePaste.defaultPrevented).toBe(false);
    expect(runtime.commands.paste).not.toHaveBeenCalled();

    const copiedPaste = pasteEvent({ text: copied });
    window.dispatchEvent(copiedPaste);
    expect(copiedPaste.defaultPrevented).toBe(true);
    expect(runtime.commands.paste).toHaveBeenCalledWith([block]);
  });

  it('cuts selected blocks only after writing them to the clipboard event', () => {
    const runtime = runtimeStub(true);
    render(<KeyboardHarness runtime={runtime} pasteImage={vi.fn((_file: File) => true)} />);
    const block = textBlock();
    useBoardStore.getState().setBlocks([block]);
    useBoardStore.getState().setSelection([block.id]);
    let copied = '';
    const cut = clipboardEvent('cut', {
      getData: () => '',
      setData: (_type, value) => { copied = value; }
    });

    window.dispatchEvent(cut);

    expect(cut.defaultPrevented).toBe(true);
    expect(importBlocks(copied, runtime.projectID)).toEqual([block]);
    expect(runtime.commands.delete).toHaveBeenCalledWith([block.id]);
    expect(useBoardStore.getState().selection.ids).toEqual([]);
  });

  it('does not intercept image paste for viewers or editable text fields', () => {
    const viewerPaste = vi.fn(() => true);
    const viewer = runtimeStub(false);
    const { unmount } = render(<KeyboardHarness runtime={viewer} pasteImage={viewerPaste} />);
    const viewerEvent = pasteEvent({ files: [new File(['png'], 'capture.png', { type: 'image/png' })] });
    window.dispatchEvent(viewerEvent);
    expect(viewerEvent.defaultPrevented).toBe(false);
    expect(viewerPaste).not.toHaveBeenCalled();
    unmount();

    const editorPaste = vi.fn(() => true);
    const editor = runtimeStub(true);
    const view = render(<KeyboardHarness runtime={editor} pasteImage={editorPaste} />);
    const textarea = view.getByRole('textbox');
    const editingEvent = pasteEvent({ files: [new File(['png'], 'capture.png', { type: 'image/png' })] });
    textarea.dispatchEvent(editingEvent);
    expect(editingEvent.defaultPrevented).toBe(false);
    expect(editorPaste).not.toHaveBeenCalled();
  });
});

function KeyboardHarness({ runtime, pasteImage }: { runtime: BoardRuntime; pasteImage: (file: File) => boolean }) {
  useBoardKeyboard(runtime, pasteImage);
  return <textarea aria-label="editor" />;
}

function runtimeStub(canEdit: boolean) {
  return {
    canEdit,
    projectID: 'project-1',
    commands: {
      paste: vi.fn(),
      undo: vi.fn(),
      redo: vi.fn(),
      delete: vi.fn(),
      duplicate: vi.fn(),
      move: vi.fn(),
      stopCapturing: vi.fn()
    }
  } as unknown as BoardRuntime;
}

function pasteEvent({ items = [], files = [], text = '' }: {
  items?: Array<{ kind: string; type: string; getAsFile: () => File | null }>;
  files?: File[];
  text?: string;
}) {
  const event = new Event('paste', { bubbles: true, cancelable: true }) as ClipboardEvent;
  Object.defineProperty(event, 'clipboardData', {
    value: { items, files, getData: (type: string) => type === 'text/plain' ? text : '' } as unknown as DataTransfer
  });
  return event;
}

function clipboardEvent(type: 'copy' | 'cut', data: Pick<DataTransfer, 'getData' | 'setData'>) {
  const event = new Event(type, { bubbles: true, cancelable: true }) as ClipboardEvent;
  Object.defineProperty(event, 'clipboardData', { value: data as DataTransfer });
  return event;
}

function textBlock(): TextBlock {
  return {
    schemaVersion: 1,
    id: 'source',
    type: 'text',
    text: 'hello',
    x: 10,
    y: 20,
    width: 200,
    height: 80,
    z: 1,
    style: { fill: 'transparent', textColor: '#111111', borderColor: '#999999', borderWidth: 0 }
  };
}
