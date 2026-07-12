type UUIDCrypto = Crypto & { randomUUID?: () => string };

let fallbackCounter = 0;

/**
 * Creates an RFC 4122 version 4 UUID without requiring a secure browser
 * context. HTTP pages cannot use crypto.randomUUID() in several browsers,
 * while crypto.getRandomValues() is more widely available. The final branch
 * keeps this helper usable without Web Crypto, although other application
 * dependencies may still require it. IDs created here must not be used as
 * authentication secrets.
 */
export function createUUID() {
  const cryptoAPI = globalThis.crypto as UUIDCrypto | undefined;
  if (typeof cryptoAPI?.randomUUID === 'function') {
    try {
      return cryptoAPI.randomUUID();
    } catch {
      // Some browsers expose randomUUID but reject it outside secure contexts.
    }
  }

  const bytes = new Uint8Array(16);
  let securelyRandom = false;
  if (typeof cryptoAPI?.getRandomValues === 'function') {
    try {
      cryptoAPI.getRandomValues(bytes);
      securelyRandom = true;
    } catch {
      // Fall through for incomplete or restricted Web Crypto implementations.
    }
  }
  if (!securelyRandom) fillCompatibilityBytes(bytes);

  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function createID(prefix: string) {
  return `${prefix}_${createUUID()}`;
}

function fillCompatibilityBytes(bytes: Uint8Array) {
  for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256);

  // Mix in time and a page-lifetime counter so consecutive calls remain
  // distinct even in browsers with a weak or stubbed Math.random().
  let timestamp = Date.now();
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = timestamp % 256;
    timestamp = Math.floor(timestamp / 256);
  }
  fallbackCounter = (fallbackCounter + 1) >>> 0;
  bytes[12] = fallbackCounter >>> 24;
  bytes[13] = fallbackCounter >>> 16;
  bytes[14] = fallbackCounter >>> 8;
  bytes[15] = fallbackCounter;
}
