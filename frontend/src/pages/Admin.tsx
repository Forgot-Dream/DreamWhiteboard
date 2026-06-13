import { FormEvent, useEffect, useState } from 'react';
import { Plus, Shield } from 'lucide-react';
import { api, type User } from '../lib/api';
import { useI18n } from '../lib/i18n';

export function Admin() {
  const { t } = useI18n();
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [systemRole, setSystemRole] = useState<'user' | 'system_admin'>('user');
  const [error, setError] = useState('');

  async function load() {
    setUsers(await api<User[]>('/api/admin/users'));
  }

  useEffect(() => {
    load().catch((err) => setError(err.message));
  }, []);

  async function createUser(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await api<User>('/api/admin/users', {
        method: 'POST',
        body: JSON.stringify({ email, name, password: 'changeme123', system_role: systemRole })
      });
      setEmail('');
      setName('');
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('admin.createFailed'));
    }
  }

  return (
    <section className="panel">
      <div className="section-head">
        <div>
          <h2>{t('admin.title')}</h2>
          <p>{t('admin.tempPassword', { password: 'changeme123' })}</p>
        </div>
        <Shield size={22} />
      </div>
      <form className="inline-form" onSubmit={createUser}>
        <input placeholder={t('admin.emailPlaceholder')} value={email} onChange={(event) => setEmail(event.target.value)} />
        <input placeholder={t('admin.namePlaceholder')} value={name} onChange={(event) => setName(event.target.value)} />
        <select value={systemRole} onChange={(event) => setSystemRole(event.target.value as 'user' | 'system_admin')}>
          <option value="user">{t('admin.role.user')}</option>
          <option value="system_admin">{t('admin.role.system_admin')}</option>
        </select>
        <button className="primary" type="submit">
          <Plus size={16} /> {t('admin.add')}
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      <div className="table">
        {users.map((user) => (
          <div className="table-row" key={user.id}>
            <span>{user.email}</span>
            <span>{user.name}</span>
            <span className="badge">{t(`admin.role.${user.system_role}`)}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
