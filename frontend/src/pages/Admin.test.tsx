import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, AUTH_EXPIRED_EVENT, type User } from '../lib/api';
import { I18nProvider } from '../lib/i18n';
import { Admin } from './Admin';

vi.mock('../lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../lib/api')>();
  return { ...original, api: vi.fn() };
});

const member: User = {
  id: 'usr_1',
  email: 'member@example.com',
  name: 'Member',
  system_role: 'user',
  must_change_password: false,
  created_at: '2026-01-01T00:00:00Z'
};
const administrator: User = {
  id: 'usr_admin',
  email: 'admin@example.com',
  name: 'Administrator',
  system_role: 'system_admin',
  must_change_password: false,
  created_at: '2025-12-15T00:00:00Z'
};
const pending: User = {
  id: 'usr_pending',
  email: 'new.user@example.com',
  name: 'New User',
  system_role: 'user',
  must_change_password: true,
  created_at: '2026-02-01T00:00:00Z'
};

describe('Admin user management', () => {
  let records: User[];

  beforeEach(() => {
    localStorage.setItem('dw_locale', 'en');
    records = [administrator, member, pending].map((user) => ({ ...user }));
    vi.mocked(api).mockImplementation(async (path, init) => {
      if (path === '/api/admin/users' && !init?.method) return records.map((user) => ({ ...user })) as never;
      if (path === '/api/admin/users' && init?.method === 'POST') {
        const input = JSON.parse(String(init.body)) as Pick<User, 'email' | 'name' | 'system_role'>;
        const created: User = { ...input, id: 'usr_created', must_change_password: true, created_at: '2026-03-01T00:00:00Z' };
        records.push(created);
        return created as never;
      }
      const userID = path.match(/^\/api\/admin\/users\/([^/]+)$/)?.[1];
      if (userID && init?.method === 'PATCH') {
        const input = JSON.parse(String(init.body)) as Partial<User>;
        const index = records.findIndex((user) => user.id === userID);
        records[index] = { ...records[index], ...input };
        return { ...records[index] } as never;
      }
      const passwordUserID = path.match(/^\/api\/admin\/users\/([^/]+)\/password$/)?.[1];
      if (passwordUserID && init?.method === 'POST') {
        const index = records.findIndex((user) => user.id === passwordUserID);
        records[index] = { ...records[index], must_change_password: true };
        return { ok: true } as never;
      }
      throw new Error(`Unexpected API request: ${path}`);
    });
  });

  afterEach(() => vi.restoreAllMocks());

  it('summarizes users and filters the directory by search, role, and password status', async () => {
    renderAdmin();
    const interaction = userEvent.setup();

    await screen.findByText(member.email);
    const totalCard = screen.getByText('Total users').closest('article');
    const adminCard = screen.getByText('System administrators').closest('article');
    const pendingCard = screen.getByText('Awaiting password change').closest('article');
    expect(totalCard && within(totalCard).getByText('3')).toBeInTheDocument();
    expect(adminCard && within(adminCard).getByText('1')).toBeInTheDocument();
    expect(pendingCard && within(pendingCard).getByText('1')).toBeInTheDocument();

    await interaction.type(screen.getByLabelText('Search users'), 'new.user');
    expect(screen.getByText(pending.email)).toBeInTheDocument();
    expect(screen.queryByText(member.email)).not.toBeInTheDocument();
    expect(screen.getByText('1 of 3 users')).toBeInTheDocument();

    await interaction.click(screen.getByRole('button', { name: 'Clear filters' }));
    await interaction.selectOptions(screen.getByLabelText('Filter users by role'), 'system_admin');
    expect(screen.getByText(administrator.email)).toBeInTheDocument();
    expect(screen.queryByText(member.email)).not.toBeInTheDocument();

    await interaction.selectOptions(screen.getByLabelText('Filter users by role'), 'all');
    await interaction.selectOptions(screen.getByLabelText('Filter users by password status'), 'pending');
    expect(screen.getByText(pending.email)).toBeInTheDocument();
    expect(screen.queryByText(administrator.email)).not.toBeInTheDocument();
  });

  it('edits display names and system roles with inline status feedback', async () => {
    const { queryClient } = renderAdmin();
    const membersKey = ['projects', 'prj_1', 'members'] as const;
    const candidatesKey = ['projects', 'prj_1', 'member-candidates'] as const;
    queryClient.setQueryData(membersKey, []);
    queryClient.setQueryData(candidatesKey, []);
    const interaction = userEvent.setup();

    await screen.findByText(member.email);
    await interaction.click(screen.getByLabelText(`Edit display name for ${member.email}`));
    const nameInput = screen.getByLabelText(`Display name for ${member.email}`);
    await interaction.clear(nameInput);
    await interaction.type(nameInput, '   ');
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    await interaction.clear(nameInput);
    await interaction.type(nameInput, 'Renamed Member');
    await interaction.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/admin/users/${member.id}`, expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ name: 'Renamed Member' })
    })));
    expect(await screen.findByText('Display name updated.')).toBeInTheDocument();
    expect(screen.getByText('Renamed Member')).toBeInTheDocument();
    await waitFor(() => expect(queryClient.getQueryState(membersKey)?.isInvalidated).toBe(true));
    expect(queryClient.getQueryState(candidatesKey)?.isInvalidated).toBe(true);

    await interaction.selectOptions(screen.getByLabelText(`System role for ${member.email}`), 'system_admin');
    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/admin/users/${member.id}`, expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ system_role: 'system_admin' })
    })));
    expect(await screen.findByText('System role updated.')).toBeInTheDocument();
  });

  it('confirms password resets and marks the account as requiring a password change', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderAdmin();
    const interaction = userEvent.setup();

    await screen.findByText(member.email);
    const baseImplementation = vi.mocked(api).getMockImplementation()!;
    let finishReset: (() => void) | undefined;
    vi.mocked(api).mockImplementation(async (path, init) => {
      if (path === `/api/admin/users/${member.id}/password` && init?.method === 'POST') {
        return new Promise((resolve) => { finishReset = () => resolve({ ok: true } as never); });
      }
      return baseImplementation(path, init);
    });
    const row = screen.getByText(member.email).closest('article');
    expect(row).not.toBeNull();
    await interaction.click(within(row!).getByRole('button', { name: 'Reset password' }));
    const password = within(row!).getByLabelText(`One-time password for ${member.email}`);
    expect(password).toHaveAttribute('type', 'password');
    await interaction.type(password, 'New-private-password-2026!');
    await interaction.click(within(row!).getByRole('button', { name: 'Confirm reset' }));

    expect(confirm).toHaveBeenCalledWith(`Reset the password for ${member.email} and sign out all of their sessions?`);
    await waitFor(() => expect(password).toBeDisabled());
    expect(within(row!).getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/admin/users/${member.id}/password`, expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ password: 'New-private-password-2026!' })
    })));
    finishReset?.();
    expect(await within(row!).findByText('Password reset. The user must change it at next login.')).toBeInTheDocument();
    expect(within(row!).getByText('Must change password')).toBeInTheDocument();
  });

  it('creates a user and reports success without waiting for a refetch', async () => {
    const { queryClient } = renderAdmin();
    const candidatesKey = ['projects', 'prj_1', 'member-candidates'] as const;
    queryClient.setQueryData(candidatesKey, []);
    const interaction = userEvent.setup();

    const createSection = screen.getByRole('heading', { name: 'Create user' }).closest('section');
    expect(createSection).not.toBeNull();
    await interaction.type(within(createSection!).getByLabelText('Email'), 'created@example.com');
    await interaction.type(within(createSection!).getByLabelText('Display name'), 'Created User');
    await interaction.type(within(createSection!).getByLabelText('One-time password'), 'Created-password-2026!');
    await interaction.click(within(createSection!).getByRole('button', { name: 'Create user' }));

    await waitFor(() => expect(api).toHaveBeenCalledWith('/api/admin/users', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ email: 'created@example.com', name: 'Created User', password: 'Created-password-2026!', system_role: 'user' })
    })));
    expect(await screen.findByText('Created created@example.com.')).toBeInTheDocument();
    expect(screen.getByText('created@example.com')).toBeInTheDocument();
    await waitFor(() => expect(queryClient.getQueryState(candidatesKey)?.isInvalidated).toBe(true));
  });

  it('warns before self-demotion and keeps the authentication cache in sync', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const { queryClient } = renderAdmin(administrator);
    const interaction = userEvent.setup();

    await screen.findByText(administrator.email);
    await interaction.selectOptions(screen.getByLabelText(`System role for ${administrator.email}`), 'user');

    expect(confirm).toHaveBeenCalledWith('Change your own role to standard user? You will immediately lose access to system administration.');
    await waitFor(() => expect(queryClient.getQueryData<User>(['auth', 'me'])?.system_role).toBe('user'));
  });

  it('uses the signed-out flow after resetting the current administrator password', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const expired = vi.fn();
    window.addEventListener(AUTH_EXPIRED_EVENT, expired);
    renderAdmin(administrator);
    const interaction = userEvent.setup();

    await screen.findByText(administrator.email);
    const row = screen.getByText(administrator.email).closest('article');
    await interaction.click(within(row!).getByRole('button', { name: 'Reset password' }));
    await interaction.type(within(row!).getByLabelText(`One-time password for ${administrator.email}`), 'Self-reset-password-2026!');
    await interaction.click(within(row!).getByRole('button', { name: 'Confirm reset' }));

    expect(confirm).toHaveBeenCalledWith('Reset your own password? You will be signed out immediately and must use the new password to sign in again.');
    await waitFor(() => expect(expired).toHaveBeenCalledTimes(1));
    window.removeEventListener(AUTH_EXPIRED_EVENT, expired);
  });
});

function renderAdmin(currentUser?: User) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  if (currentUser) queryClient.setQueryData(['auth', 'me'], currentUser);
  return { queryClient, ...render(<QueryClientProvider client={queryClient}><I18nProvider><Admin /></I18nProvider></QueryClientProvider>) };
}
