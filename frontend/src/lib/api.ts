export type SystemRole = 'system_admin' | 'user';
export type ProjectRole = 'owner' | 'admin' | 'editor' | 'viewer';
export type BlockType = 'rich_text' | 'note' | 'image' | 'shape';

export interface User {
  id: string;
  email: string;
  name: string;
  system_role: SystemRole;
  created_at: string;
}

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
  version: number;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface WhiteboardBlock {
  id: string;
  type: BlockType;
  x: number;
  y: number;
  w: number;
  h: number;
  z: number;
  data: Record<string, unknown>;
}

export interface BoardSnapshot {
  board_id: string;
  version: number;
  blocks: WhiteboardBlock[];
  updated_at: string;
}

export interface Asset {
  id: string;
  project_id: string;
  file_name: string;
  content_type: string;
  size: number;
  path: string;
  created_at: string;
}

const API_BASE = import.meta.env.VITE_API_BASE ?? '';

let token = localStorage.getItem('dw_token') ?? '';

export function setToken(next: string) {
  token = next;
  if (next) localStorage.setItem('dw_token', next);
  else localStorage.removeItem('dw_token');
}

export function getToken() {
  return token;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (!(init.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers,
    credentials: 'include'
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error ?? `Request failed: ${response.status}`);
  }
  return response.json();
}

export function wsURL(boardID: string, clientID: string) {
  const base = API_BASE || window.location.origin;
  const url = new URL(`/api/boards/${boardID}/ws`, base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('client_id', clientID);
  if (token) url.searchParams.set('token', token);
  return url.toString();
}

export function assetURL(assetID: string) {
  const base = API_BASE || window.location.origin;
  const url = new URL(`/api/assets/${assetID}`, base);
  if (token) url.searchParams.set('token', token);
  return url.toString();
}

export function canEdit(role?: ProjectRole, user?: User) {
  return user?.system_role === 'system_admin' || role === 'owner' || role === 'admin' || role === 'editor';
}

export function canManage(role?: ProjectRole, user?: User) {
  return user?.system_role === 'system_admin' || role === 'owner' || role === 'admin';
}
