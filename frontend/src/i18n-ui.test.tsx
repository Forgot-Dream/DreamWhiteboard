import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { BoardToolbar } from './board/BoardToolbar';
import { CanvasViewport } from './board/CanvasViewport';
import type { BoardRuntime } from './board/runtime';
import type { TextBlock } from './board/schema';
import { useBoardStore } from './board/store';
import { APIError, api, type Board, type Project, type User } from './lib/api';
import { I18nProvider, useI18n } from './lib/i18n';
import { Admin } from './pages/Admin';
import { Projects } from './pages/Projects';

vi.mock('./lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('./lib/api')>();
  return { ...original, api: vi.fn() };
});

const user: User = {
  id: 'usr_admin', email: 'admin@example.com', name: 'Admin', system_role: 'system_admin', created_at: '2026-01-01T00:00:00Z'
};
const project: Project = {
  id: 'prj_1', name: 'Project Alpha', description: '', created_by: user.id, created_at: '2026-01-01T00:00:00Z'
};
const board: Board = {
  id: 'brd_1', project_id: project.id, name: 'Board Alpha', created_by: user.id,
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z'
};

describe('Simplified Chinese UI coverage', () => {
  beforeEach(() => {
    localStorage.setItem('dw_locale', 'zh_cn');
    useBoardStore.getState().reset(board.id);
    vi.mocked(api).mockImplementation(async (path) => {
      if (path === '/api/projects') return [project] as never;
      if (path === `/api/projects/${project.id}`) return project as never;
      if (path === `/api/projects/${project.id}/boards`) return [] as never;
      if (path === `/api/projects/${project.id}/members`) return [{ project_id: project.id, user_id: user.id, role: 'owner', user, created_at: user.created_at }] as never;
      if (path === `/api/projects/${project.id}/member-candidates`) return [] as never;
      if (path === '/api/admin/users') return [user] as never;
      throw new Error(`Unexpected API request: ${path}`);
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('localizes project management controls and empty states', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[`/projects/${project.id}`]}>
          <I18nProvider>
            <Routes><Route path="/projects/:projectId" element={<Projects user={user} />} /></Routes>
          </I18nProvider>
        </MemoryRouter>
      </QueryClientProvider>
    );

    expect(await screen.findByPlaceholderText('项目名称')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('描述（可选）')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建' })).toBeInTheDocument();
    expect(await screen.findByPlaceholderText('白板名称')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '白板' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '成员' })).toBeInTheDocument();
    expect(await screen.findByRole('combobox', { name: '按姓名或邮箱搜索用户' })).toBeInTheDocument();
    expect(screen.getAllByRole('option', { name: '所有者' })).not.toHaveLength(0);
    expect(await screen.findByText('暂无白板。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '编辑项目' })).toBeInTheDocument();
  });

  it('localizes the enhanced administration directory', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><I18nProvider><Admin /></I18nProvider></QueryClientProvider>);

    expect(await screen.findByRole('heading', { name: '系统用户' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '新增用户' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '用户目录' })).toBeInTheDocument();
    expect(screen.getByText('用户总数')).toBeInTheDocument();
    expect(screen.getByLabelText('搜索用户')).toBeInTheDocument();
    expect(screen.getByLabelText('按角色筛选用户')).toBeInTheDocument();
    expect(screen.getByLabelText('按密码状态筛选用户')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建用户' })).toBeInTheDocument();
  });

  it('localizes whiteboard toolbar labels and connection status', () => {
    const runtime = { canEdit: true, commands: {} } as unknown as BoardRuntime;
    useBoardStore.getState().setConnectionError('opaque collaboration failure');
    render(
      <MemoryRouter>
        <I18nProvider><BoardToolbar runtime={runtime} board={board} onBackProjectID={project.id} imageUpload={{ status: null, upload: vi.fn(), cancel: vi.fn(), retry: vi.fn() }} /></I18nProvider>
      </MemoryRouter>
    );

    expect(screen.getByTitle('返回')).toBeInTheDocument();
    expect(screen.getByRole('toolbar', { name: '白板工具' })).toBeInTheDocument();
    expect(screen.getByTitle('选择（V）')).toBeInTheDocument();
    expect(screen.getByTitle('平移')).toBeInTheDocument();
    expect(screen.getByTitle('文本')).toBeInTheDocument();
    expect(screen.getByTitle('上传图片')).toBeInTheDocument();
    expect(screen.getByTitle('撤销')).toBeInTheDocument();
    expect(screen.getByTitle('重做')).toBeInTheDocument();
    expect(screen.getByTitle('缩放至全部内容')).toBeInTheDocument();
    expect(screen.getByText('项目 · 连接中')).toBeInTheDocument();
    expect(screen.getByText('协作发生错误。')).toBeInTheDocument();
  });

  it('localizes stable API permission error codes', () => {
    render(
      <I18nProvider>
        <ErrorMessage error={new APIError(403, { error: { code: 'project_access_denied', message: 'project access denied' } })} />
      </I18nProvider>
    );
    expect(screen.getByText('你无权访问该项目。')).toBeInTheDocument();
  });

  it('keeps rendering when locale storage is unavailable', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new DOMException('Storage disabled', 'SecurityError'); },
      setItem: () => { throw new DOMException('Storage disabled', 'SecurityError'); }
    });
    render(<I18nProvider><span>storage-safe</span></I18nProvider>);
    expect(screen.getByText('storage-safe')).toBeInTheDocument();
  });

  it('localizes canvas statistics and block resize labels', () => {
    const block: TextBlock = {
      schemaVersion: 1, id: 'txt_1', type: 'text', text: 'Hello', x: 0, y: 0, width: 200, height: 80, z: 1,
      style: { fill: 'transparent', textColor: '#000000', borderColor: '#999999', borderWidth: 0 }
    };
    useBoardStore.getState().setBlocks([block]);
    useBoardStore.getState().setSelection([block.id]);
    vi.stubGlobal('ResizeObserver', class {
      observe() { /* no-op */ }
      disconnect() { /* no-op */ }
    });
    const runtime = {
      canEdit: true,
      commands: {},
      provider: { updateLocalPresence: vi.fn() }
    } as unknown as BoardRuntime;

    render(<I18nProvider><CanvasViewport runtime={runtime} /></I18nProvider>);

    expect(screen.getByText('共 1 个物块 · 已渲染 1 个')).toBeInTheDocument();
    expect(screen.getByLabelText('移动文本物块')).toBeInTheDocument();
    expect(screen.getByLabelText('调整上边缘')).toBeInTheDocument();
    expect(screen.getByLabelText('调整右下角')).toBeInTheDocument();
  });
});

function ErrorMessage({ error }: { error: unknown }) {
  const { errorMessage } = useI18n();
  return <span>{errorMessage(error)}</span>;
}
