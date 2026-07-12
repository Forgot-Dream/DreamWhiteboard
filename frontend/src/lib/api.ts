export type SystemRole = 'system_admin' | 'user';
export type ProjectRole = 'owner' | 'admin' | 'editor' | 'viewer';

export interface User {
  id: string;
  email: string;
  name: string;
  system_role: SystemRole;
  must_change_password?: boolean;
  created_at: string;
}

export type MemberCandidate = Pick<User, 'id' | 'email' | 'name'>;

export interface Project {
  id: string;
  name: string;
  description: string;
  created_by: string;
  created_at: string;
}

export interface ProjectMember {
  project_id: string;
  user_id: string;
  role: ProjectRole;
  user?: User;
  created_at: string;
}

export interface Board {
  id: string;
  project_id: string;
  name: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  // Kept optional while v1 deployments are upgraded. Document versions live in Yjs.
  version?: number;
}

export interface BoardAccess {
  board: Board;
  project?: Project;
  permission?: {
    role?: ProjectRole;
    can_edit: boolean;
    can_manage?: boolean;
  };
  collaboration_endpoint?: string;
}

export interface Asset {
  id: string;
  project_id: string;
  file_name: string;
  content_type: string;
  size: number;
  sha256?: string;
  width?: number;
  height?: number;
  created_at: string;
}

export interface APIErrorBody {
  error?: string | { code?: string; message?: string; details?: unknown; fields?: Record<string, string>; request_id?: string };
  code?: string;
  message?: string;
  request_id?: string;
}

export class APIError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly requestID?: string;
  readonly details?: unknown;

  constructor(status: number, body: APIErrorBody) {
    const nested = typeof body.error === 'object' ? body.error : undefined;
    super(nested?.message ?? body.message ?? (typeof body.error === 'string' ? body.error : `Request failed: ${status}`));
    this.name = 'APIError';
    this.status = status;
    this.code = nested?.code ?? body.code;
    this.requestID = nested?.request_id ?? body.request_id;
    this.details = nested?.details ?? nested?.fields;
  }
}

const API_BASE = (import.meta.env.VITE_API_BASE ?? '').replace(/\/$/, '');
export const AUTH_EXPIRED_EVENT = 'dreamwhiteboard:auth-expired';

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body !== undefined && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers,
    credentials: 'include'
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({} as APIErrorBody)) as APIErrorBody;
    if (response.status === 401 && path !== '/api/auth/login') window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
    throw new APIError(response.status, body);
  }
  if (response.status === 204) return undefined as T;
  const contentType = response.headers.get('Content-Type') ?? '';
  if (!contentType.includes('application/json')) return undefined as T;
  return response.json() as Promise<T>;
}

export function wsURL(boardID: string, clientID?: string) {
  const base = API_BASE || window.location.origin;
  const url = new URL(`/api/boards/${encodeURIComponent(boardID)}/ws`, base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  if (clientID) url.searchParams.set('client_id', clientID);
  return url.toString();
}

export function assetURL(assetID: string) {
  return `${API_BASE}/api/assets/${encodeURIComponent(assetID)}`;
}

export function canEdit(role?: ProjectRole, user?: User, explicit?: boolean) {
  if (typeof explicit === 'boolean') return explicit;
  return user?.system_role === 'system_admin' || role === 'owner' || role === 'admin' || role === 'editor';
}

export function canManage(role?: ProjectRole, user?: User) {
  return user?.system_role === 'system_admin' || role === 'owner' || role === 'admin';
}

export function canManageOwners(role?: ProjectRole, user?: User) {
  return user?.system_role === 'system_admin' || role === 'owner';
}

export function uploadAsset(
  projectID: string,
  file: File,
  options: { signal?: AbortSignal; onProgress?: (percent: number) => void } = {}
): Promise<Asset> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open('POST', `${API_BASE}/api/projects/${encodeURIComponent(projectID)}/assets`);
    request.responseType = 'json';
    request.withCredentials = true;
    request.setRequestHeader('Accept', 'application/json');
    request.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(Math.round((event.loaded / event.total) * 100));
    };
    request.onload = () => {
      if (request.status >= 200 && request.status < 300) {
        resolve(request.response as Asset);
        return;
      }
      const body = (request.response ?? {}) as APIErrorBody;
      reject(new APIError(request.status, body));
    };
    request.onerror = () => reject(new Error('Upload failed'));
    request.onabort = () => reject(new DOMException('Upload cancelled', 'AbortError'));
    options.signal?.addEventListener('abort', () => request.abort(), { once: true });
    const form = new FormData();
    form.append('file', file);
    request.send(form);
  });
}
