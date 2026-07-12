import { FormEvent, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Navigate, useNavigate } from 'react-router-dom';
import { authQueryKey } from '../App';
import { api, type User } from '../lib/api';
import { LocaleSelect, useI18n } from '../lib/i18n';

export function ChangePassword({ user }: { user: User }) {
  const { errorMessage, t } = useI18n();
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const mutation = useMutation({
    mutationFn: () => api<{ user: User }>('/api/me/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword })
    }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: authQueryKey });
      navigate('/projects', { replace: true });
    }
  });

  if (!user.must_change_password) return <Navigate to="/projects" replace />;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (newPassword !== confirmation) return;
    mutation.mutate();
  }

  return (
    <main className="login-shell">
      <form className="login-panel" onSubmit={submit}>
        <LocaleSelect />
        <div><h1>{t('password.title')}</h1><p>{t('password.subtitle')}</p></div>
        <label>{t('password.current')}<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label>
        <label>{t('password.new')}<input type="password" autoComplete="new-password" minLength={12} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label>
        <label>{t('password.confirm')}<input type="password" autoComplete="new-password" minLength={12} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label>
        {newPassword !== confirmation && confirmation && <p className="error">{t('password.mismatch')}</p>}
        {mutation.error && <p className="error">{errorMessage(mutation.error)}</p>}
        <button className="primary" disabled={mutation.isPending || newPassword !== confirmation}>{t('password.save')}</button>
      </form>
    </main>
  );
}
