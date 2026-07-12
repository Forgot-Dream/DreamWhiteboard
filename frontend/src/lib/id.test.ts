import { afterEach, describe, expect, it, vi } from 'vitest';
import { createID, createUUID } from './id';

describe('createUUID', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uses the native implementation when available', () => {
    const nativeUUID = '12345678-1234-4123-8123-123456789abc';
    const randomUUID = vi.fn(() => nativeUUID);
    vi.stubGlobal('crypto', { randomUUID });

    expect(createUUID()).toBe(nativeUUID);
    expect(randomUUID).toHaveBeenCalledOnce();
  });

  it('builds a version 4 UUID with getRandomValues on HTTP-compatible browsers', () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.forEach((_, index) => { bytes[index] = index; });
        return bytes;
      }
    });

    expect(createUUID()).toBe('00010203-0405-4607-8809-0a0b0c0d0e0f');
  });

  it('falls back when an exposed randomUUID is rejected', () => {
    vi.stubGlobal('crypto', {
      randomUUID: () => { throw new DOMException('Secure context required', 'SecurityError'); },
      getRandomValues: (bytes: Uint8Array) => {
        bytes.fill(0xab);
        return bytes;
      }
    });

    expect(createUUID()).toBe('abababab-abab-4bab-abab-abababababab');
  });

  it('still creates distinct UUIDs without Web Crypto', () => {
    vi.stubGlobal('crypto', undefined);
    vi.spyOn(Date, 'now').mockReturnValue(1_725_000_000_000);
    vi.spyOn(Math, 'random').mockReturnValue(0);

    const first = createUUID();
    const second = createUUID();

    expect(first).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(second).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(second).not.toBe(first);
  });

  it('adds the requested identifier prefix', () => {
    vi.stubGlobal('crypto', { randomUUID: () => '12345678-1234-4123-8123-123456789abc' });
    expect(createID('blk')).toBe('blk_12345678-1234-4123-8123-123456789abc');
  });
});
