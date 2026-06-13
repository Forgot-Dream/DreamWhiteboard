import { FormEvent, useState } from 'react';
import { LogIn } from 'lucide-react';
import { api, setToken, type User } from '../lib/api';
import { LocaleSelect, useI18n } from '../lib/i18n';

interface LoginProps {
  onLogin: (user: User) => void;
}

export function Login({ onLogin }: LoginProps) {
  const { t } = useI18n();
  const [email, setEmail] = useState('admin@example.com');
  const [password, setPassword] = useState('admin123');
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      const result = await api<{ token: string; user: User }>('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password })
      });
      setToken(result.token);
      onLogin(result.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('login.failed'));
    }
  }

  return (
    <main className="login-shell">
      <form className="login-panel" onSubmit={submit}>
        <LocaleSelect />
        <div>
          <h1>DreamWhiteboard</h1>
          <p>{t('login.subtitle')}</p>
        </div>
        <label>
          {t('login.email')}
          <input value={email} onChange={(event) => setEmail(event.target.value)} />
        </label>
        <label>
          {t('login.password')}
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" type="submit">
          <LogIn size={18} /> {t('login.signIn')}
        </button>
      </form>
    </main>
  );
}
