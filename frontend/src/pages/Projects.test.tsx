import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { api, type Project, type ProjectMember, type User } from '../lib/api';
import { I18nProvider } from '../lib/i18n';
import { Projects } from './Projects';

vi.mock('../lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../lib/api')>();
  return { ...original, api: vi.fn() };
});

const owner: User = {
  id: 'usr_owner',
  email: 'owner-with-a-very-long-address@example.com',
  name: 'Workspace Owner With A Long Name',
  system_role: 'user',
  created_at: '2026-01-01T00:00:00Z'
};
const alice: User = {
  id: 'usr_alice', email: 'alice@example.com', name: 'Alice Builder', system_role: 'user', created_at: '2026-01-01T00:00:00Z'
};
const bob: User = {
  id: 'usr_bob', email: 'bob@example.com', name: 'Bob Reviewer', system_role: 'user', created_at: '2026-01-01T00:00:00Z'
};
const project: Project = {
  id: 'prj_1', name: 'Project Alpha', description: '', created_by: owner.id, created_at: '2026-01-01T00:00:00Z'
};
const ownerMember: ProjectMember = {
  project_id: project.id, user_id: owner.id, role: 'owner', user: owner, created_at: owner.created_at
};

function renderProjects() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[`/projects/${project.id}`]}>
        <I18nProvider>
          <Routes><Route path="/projects/:projectId" element={<Projects user={owner} />} /></Routes>
        </I18nProvider>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe('Project member controls', () => {
  beforeEach(() => {
    localStorage.setItem('dw_locale', 'en');
    vi.mocked(api).mockImplementation(async (path, init) => {
      if (path === '/api/projects' && !init?.method) return [project] as never;
      if (path === `/api/projects/${project.id}` && !init?.method) return project as never;
      if (path === `/api/projects/${project.id}/boards` && !init?.method) return [] as never;
      if (path === `/api/projects/${project.id}/members` && !init?.method) return [ownerMember] as never;
      if (path === `/api/projects/${project.id}/member-candidates` && !init?.method) return [alice, bob] as never;
      if (path === `/api/projects/${project.id}/members` && init?.method === 'POST') {
        return { project_id: project.id, user_id: bob.id, role: 'viewer', user: bob, created_at: bob.created_at } as never;
      }
      throw new Error(`Unexpected API request: ${path}`);
    });
  });

  it('filters users by name or email, excludes existing members, and adds only a selected result', async () => {
    renderProjects();
    const interaction = userEvent.setup();
    const combobox = await screen.findByRole('combobox', { name: 'Search users by name or email' });
    const addButton = screen.getByRole('button', { name: 'Add' });

    expect(addButton).toBeDisabled();
    await interaction.click(combobox);
    const results = screen.getByRole('listbox', { name: 'Matching users' });
    expect(within(results).queryByRole('option', { name: /Workspace Owner/ })).not.toBeInTheDocument();
    expect(within(results).getByRole('option', { name: /Alice Builder/ })).toHaveAttribute('tabindex', '-1');

    await interaction.type(combobox, 'bob@example.com');
    expect(within(results).queryByRole('option', { name: /Alice Builder/ })).not.toBeInTheDocument();
    expect(within(results).getByRole('option', { name: /Bob Reviewer/ })).toBeInTheDocument();
    expect(addButton).toBeDisabled();

    await interaction.keyboard('{ArrowDown}{Enter}');
    expect(combobox).toHaveValue('Bob Reviewer · bob@example.com');
    expect(addButton).toBeEnabled();
    await interaction.selectOptions(screen.getByRole('combobox', { name: 'New member role' }), 'viewer');
    await interaction.click(addButton);

    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/projects/${project.id}/members`, expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ user_id: bob.id, role: 'viewer' })
    })));
    await waitFor(() => expect(combobox).toHaveValue(''));
    expect(addButton).toBeDisabled();
    await waitFor(() => expect(vi.mocked(api).mock.calls.filter(([path]) => path === `/api/projects/${project.id}/member-candidates`).length).toBeGreaterThanOrEqual(2));
    expect(api).not.toHaveBeenCalledWith('/api/admin/users');
  });

  it('shows a localized empty result and presents member identity separately from its role control', async () => {
    renderProjects();
    const interaction = userEvent.setup();
    const combobox = await screen.findByRole('combobox', { name: 'Search users by name or email' });

    await interaction.type(combobox, 'ALICE BUILDER');
    expect(screen.getByRole('option', { name: /Alice Builder/ })).toBeInTheDocument();
    await interaction.clear(combobox);
    await interaction.type(combobox, 'no-such-user');
    expect(screen.getByText('No matching users.')).toBeInTheDocument();

    const identity = screen.getByText(owner.name).closest('.member-identity');
    expect(identity).toHaveAttribute('title', `${owner.name} · ${owner.email}`);
    expect(screen.getByText(owner.email)).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: `Project role for ${owner.email}` })).toBeInTheDocument();
  });
});
