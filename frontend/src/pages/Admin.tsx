import { FormEvent, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Plus, Shield } from 'lucide-react';
import { api, type SystemRole, type User } from '../lib/api';
import { useI18n } from '../lib/i18n';

const usersKey = ['admin', 'users'] as const;
const systemRoleMessageKeys = { user: 'admin.role.user', system_admin: 'admin.role.system_admin' } as const;

export function Admin() {
  const { errorMessage, t } = useI18n();
  const queryClient = useQueryClient();
  const users = useQuery({ queryKey: usersKey, queryFn: () => api<User[]>('/api/admin/users') });
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [systemRole, setSystemRole] = useState<SystemRole>('user');
  const create = useMutation({
    mutationFn: () => api<User>('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify({ email, name, password, system_role: systemRole })
    }),
    onSuccess: async () => {
      setEmail(''); setName(''); setPassword('');
      await queryClient.invalidateQueries({ queryKey: usersKey });
    }
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    create.mutate();
  }

  return (
    <section className="panel admin-panel">
      <div className="section-head"><div><h2>{t('admin.title')}</h2><p>{t('admin.subtitle')}</p></div><Shield size={22} /></div>
      <form className="inline-form admin-user-form" onSubmit={submit}>
        <input type="email" placeholder={t('admin.emailPlaceholder')} value={email} onChange={(event) => setEmail(event.target.value)} required />
        <input placeholder={t('admin.namePlaceholder')} value={name} onChange={(event) => setName(event.target.value)} required />
        <input type="password" minLength={12} placeholder={t('admin.passwordPlaceholder')} value={password} onChange={(event) => setPassword(event.target.value)} required />
        <select value={systemRole} onChange={(event) => setSystemRole(event.target.value as SystemRole)}><option value="user">{t('admin.role.user')}</option><option value="system_admin">{t('admin.role.system_admin')}</option></select>
        <button className="primary" disabled={create.isPending}><Plus size={16} /> {t('admin.add')}</button>
      </form>
      {(users.error || create.error) && <p className="error">{errorMessage(users.error ?? create.error)}</p>}
      <div className="table">
        {users.data?.map((user) => <UserRow key={user.id} user={user} />)}
        {!users.isLoading && users.data?.length === 0 && <div className="empty-state">{t('admin.empty')}</div>}
      </div>
    </section>
  );
}

function UserRow({ user }: { user: User }) {
  const { errorMessage, t } = useI18n();
  const queryClient = useQueryClient();
  const [resetting, setResetting] = useState(false);
  const [password, setPassword] = useState('');
  const update = useMutation({
    mutationFn: (role: SystemRole) => api<User>(`/api/admin/users/${encodeURIComponent(user.id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ system_role: role })
    }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersKey })
  });
  const reset = useMutation({
    mutationFn: () => api(`/api/admin/users/${encodeURIComponent(user.id)}/password`, {
      method: 'POST',
      body: JSON.stringify({ password })
    }),
    onSuccess: async () => {
      setPassword('');
      setResetting(false);
      await queryClient.invalidateQueries({ queryKey: usersKey });
    }
  });
  return (
    <div className="table-row user-row">
      <span>{user.email}</span><span>{user.name}</span>
      <select aria-label={t('admin.systemRoleFor', { email: user.email })} value={user.system_role} disabled={update.isPending} onChange={(event) => update.mutate(event.target.value as SystemRole)}>
        <option value="user">{t(systemRoleMessageKeys.user)}</option><option value="system_admin">{t(systemRoleMessageKeys.system_admin)}</option>
      </select>
      {resetting ? (
        <form className="inline-form password-reset-form" onSubmit={(event) => { event.preventDefault(); reset.mutate(); }}>
          <input aria-label={t('admin.oneTimePasswordFor', { email: user.email })} type="password" minLength={12} value={password} onChange={(event) => setPassword(event.target.value)} required autoFocus />
          <button className="small-btn" disabled={reset.isPending}>{t('common.save')}</button>
          <button type="button" className="small-btn" onClick={() => { setPassword(''); setResetting(false); }}>{t('common.cancel')}</button>
        </form>
      ) : <button className="small-btn" onClick={() => setResetting(true)} title={t('admin.resetPassword')}><KeyRound size={15} /></button>}
      {(update.error || reset.error) && <span className="error row-error">{errorMessage(update.error ?? reset.error)}</span>}
    </div>
  );
}
