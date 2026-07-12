import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type User } from '../lib/api';
import { I18nProvider } from '../lib/i18n';
import { Admin } from './Admin';

vi.mock('../lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../lib/api')>();
  return { ...original, api: vi.fn() };
});

const user: User = {
  id: 'usr_1',
  email: 'member@example.com',
  name: 'Member',
  system_role: 'user',
  created_at: '2026-01-01T00:00:00Z'
};

describe('Admin user controls', () => {
  beforeEach(() => {
    vi.mocked(api).mockImplementation(async (path, init) => {
      if (path === '/api/admin/users' && !init?.method) return [user] as never;
      if (path === `/api/admin/users/${user.id}` && init?.method === 'PATCH') {
        return { ...user, system_role: 'system_admin' } as never;
      }
      if (path === `/api/admin/users/${user.id}/password` && init?.method === 'POST') return { ok: true } as never;
      throw new Error(`Unexpected API request: ${path}`);
    });
  });

  it('edits system roles and uses an inline masked password reset form', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><I18nProvider><Admin /></I18nProvider></QueryClientProvider>);
    const interaction = userEvent.setup();

    await screen.findByText(user.email);
    await interaction.selectOptions(screen.getByLabelText(`System role for ${user.email}`), 'system_admin');
    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/admin/users/${user.id}`, expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ system_role: 'system_admin' })
    })));

    await interaction.click(screen.getByTitle('Reset password'));
    const password = screen.getByLabelText(`One-time password for ${user.email}`);
    expect(password).toHaveAttribute('type', 'password');
    await interaction.type(password, 'New-private-password-2026!');
    await interaction.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api).toHaveBeenCalledWith(`/api/admin/users/${user.id}/password`, expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ password: 'New-private-password-2026!' })
    })));
  });
});
