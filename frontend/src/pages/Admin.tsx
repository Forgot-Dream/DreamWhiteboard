import { FormEvent, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Plus, Shield } from 'lucide-react';
import { api, type SystemRole, type User } from '../lib/api';

const usersKey = ['admin', 'users'] as const;

export function Admin() {
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
      <div className="section-head"><div><h2>System users</h2><p>New users must replace the one-time password at first login.</p></div><Shield size={22} /></div>
      <form className="inline-form admin-user-form" onSubmit={submit}>
        <input type="email" placeholder="email" value={email} onChange={(event) => setEmail(event.target.value)} required />
        <input placeholder="name" value={name} onChange={(event) => setName(event.target.value)} required />
        <input type="password" minLength={12} placeholder="one-time password" value={password} onChange={(event) => setPassword(event.target.value)} required />
        <select value={systemRole} onChange={(event) => setSystemRole(event.target.value as SystemRole)}><option value="user">user</option><option value="system_admin">system_admin</option></select>
        <button className="primary" disabled={create.isPending}><Plus size={16} /> Add</button>
      </form>
      {(users.error || create.error) && <p className="error">{users.error?.message ?? create.error?.message}</p>}
      <div className="table">
        {users.data?.map((user) => <UserRow key={user.id} user={user} />)}
        {!users.isLoading && users.data?.length === 0 && <div className="empty-state">No users yet.</div>}
      </div>
    </section>
  );
}

function UserRow({ user }: { user: User }) {
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
      <select aria-label={`System role for ${user.email}`} value={user.system_role} disabled={update.isPending} onChange={(event) => update.mutate(event.target.value as SystemRole)}>
        <option value="user">user</option><option value="system_admin">system_admin</option>
      </select>
      {resetting ? (
        <form className="inline-form password-reset-form" onSubmit={(event) => { event.preventDefault(); reset.mutate(); }}>
          <input aria-label={`One-time password for ${user.email}`} type="password" minLength={12} value={password} onChange={(event) => setPassword(event.target.value)} required autoFocus />
          <button className="small-btn" disabled={reset.isPending}>Save</button>
          <button type="button" className="small-btn" onClick={() => { setPassword(''); setResetting(false); }}>Cancel</button>
        </form>
      ) : <button className="small-btn" onClick={() => setResetting(true)} title="Reset password"><KeyRound size={15} /></button>}
      {(update.error || reset.error) && <span className="error row-error">{update.error?.message ?? reset.error?.message}</span>}
    </div>
  );
}
