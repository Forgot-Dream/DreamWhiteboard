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
  const reset = useMutation({
    mutationFn: async () => {
      const next = window.prompt(`Set a one-time password for ${user.email} (12+ characters)`);
      if (!next) return;
      await api(`/api/admin/users/${encodeURIComponent(user.id)}/password`, { method: 'POST', body: JSON.stringify({ password: next }) });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersKey })
  });
  return (
    <div className="table-row user-row">
      <span>{user.email}</span><span>{user.name}</span><span className="badge">{user.system_role}</span>
      <button className="small-btn" onClick={() => reset.mutate()} disabled={reset.isPending} title="Reset password"><KeyRound size={15} /></button>
    </div>
  );
}
