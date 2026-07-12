import { FormEvent, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { LogIn } from 'lucide-react';
import { useLocation, useNavigate } from 'react-router-dom';
import { authQueryKey } from '../App';
import { api, type User } from '../lib/api';
import { LocaleSelect, useI18n } from '../lib/i18n';

export function Login() {
  const { errorMessage, t } = useI18n();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const mutation = useMutation({
    mutationFn: () => api<{ user: User }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password })
    }),
    onSuccess: ({ user }) => {
      queryClient.setQueryData(authQueryKey, user);
      const state = location.state as { from?: string } | null;
      navigate(user.must_change_password ? '/change-password' : state?.from ?? '/projects', { replace: true });
    }
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    mutation.mutate();
  }

  return (
    <main className="login-shell">
      <form className="login-panel" onSubmit={submit}>
        <LocaleSelect />
        <div><h1>DreamWhiteboard</h1><p>{t('login.subtitle')}</p></div>
        <label>{t('login.email')}<input type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} required autoFocus /></label>
        <label>{t('login.password')}<input type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
        {mutation.error && <p className="error" role="alert">{errorMessage(mutation.error, 'login.failed')}</p>}
        <button className="primary" type="submit" disabled={mutation.isPending}>
          <LogIn size={18} /> {mutation.isPending ? `${t('login.signIn')}…` : t('login.signIn')}
        </button>
      </form>
    </main>
  );
}
