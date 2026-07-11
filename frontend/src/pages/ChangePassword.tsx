import { FormEvent, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Navigate, useNavigate } from 'react-router-dom';
import { authQueryKey } from '../App';
import { api, type User } from '../lib/api';

export function ChangePassword({ user }: { user: User }) {
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
        <div><h1>Change password</h1><p>Choose a private password before continuing.</p></div>
        <label>Current password<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label>
        <label>New password<input type="password" autoComplete="new-password" minLength={12} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label>
        <label>Confirm password<input type="password" autoComplete="new-password" minLength={12} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label>
        {newPassword !== confirmation && confirmation && <p className="error">Passwords do not match.</p>}
        {mutation.error && <p className="error">{mutation.error.message}</p>}
        <button className="primary" disabled={mutation.isPending || newPassword !== confirmation}>Save password</button>
      </form>
    </main>
  );
}
