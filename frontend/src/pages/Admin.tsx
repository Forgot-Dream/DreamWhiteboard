import { FormEvent, useEffect, useState } from 'react';
import { Plus, Shield } from 'lucide-react';
import { api, type User } from '../lib/api';

export function Admin() {
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
      setError(err instanceof Error ? err.message : 'Create user failed');
    }
  }

  return (
    <section className="panel">
      <div className="section-head">
        <div>
          <h2>System Users</h2>
          <p>Created users receive the temporary password <code>changeme123</code>.</p>
        </div>
        <Shield size={22} />
      </div>
      <form className="inline-form" onSubmit={createUser}>
        <input placeholder="email" value={email} onChange={(event) => setEmail(event.target.value)} />
        <input placeholder="name" value={name} onChange={(event) => setName(event.target.value)} />
        <select value={systemRole} onChange={(event) => setSystemRole(event.target.value as 'user' | 'system_admin')}>
          <option value="user">user</option>
          <option value="system_admin">system_admin</option>
        </select>
        <button className="primary" type="submit">
          <Plus size={16} /> Add
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      <div className="table">
        {users.map((user) => (
          <div className="table-row" key={user.id}>
            <span>{user.email}</span>
            <span>{user.name}</span>
            <span className="badge">{user.system_role}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

