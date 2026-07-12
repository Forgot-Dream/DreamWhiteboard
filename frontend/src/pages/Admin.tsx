import { FormEvent, useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { CalendarDays, CheckCircle2, Clock3, KeyRound, Pencil, Plus, Search, Shield, ShieldCheck, UserRound, Users, X } from 'lucide-react';
import { api, AUTH_EXPIRED_EVENT, type SystemRole, type User } from '../lib/api';
import { useI18n } from '../lib/i18n';

const usersKey = ['admin', 'users'] as const;
const systemRoleMessageKeys = { user: 'admin.role.user', system_admin: 'admin.role.system_admin' } as const;
type RoleFilter = 'all' | SystemRole;
type PasswordFilter = 'all' | 'active' | 'pending';

export function Admin() {
  const { errorMessage, t } = useI18n();
  const queryClient = useQueryClient();
  const currentUserID = queryClient.getQueryData<User>(['auth', 'me'])?.id;
  const users = useQuery({ queryKey: usersKey, queryFn: () => api<User[]>('/api/admin/users') });
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [systemRole, setSystemRole] = useState<SystemRole>('user');
  const [search, setSearch] = useState('');
  const [roleFilter, setRoleFilter] = useState<RoleFilter>('all');
  const [passwordFilter, setPasswordFilter] = useState<PasswordFilter>('all');
  const [notice, setNotice] = useState('');

  const allUsers = users.data ?? [];
  const filteredUsers = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return allUsers.filter((user) => {
      const matchesQuery = !query || user.email.toLocaleLowerCase().includes(query) || user.name.toLocaleLowerCase().includes(query);
      const matchesRole = roleFilter === 'all' || user.system_role === roleFilter;
      const matchesPassword = passwordFilter === 'all'
        || (passwordFilter === 'pending' ? Boolean(user.must_change_password) : !user.must_change_password);
      return matchesQuery && matchesRole && matchesPassword;
    });
  }, [allUsers, passwordFilter, roleFilter, search]);
  const administratorCount = allUsers.filter((user) => user.system_role === 'system_admin').length;
  const pendingPasswordCount = allUsers.filter((user) => user.must_change_password).length;
  const hasFilters = Boolean(search) || roleFilter !== 'all' || passwordFilter !== 'all';

  const create = useMutation({
    mutationFn: () => api<User>('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify({ email, name, password, system_role: systemRole })
    }),
    onMutate: () => setNotice(''),
    onSuccess: async (created) => {
      queryClient.setQueryData<User[]>(usersKey, (current = []) => [...current, created].sort((left, right) => left.email.localeCompare(right.email)));
      setEmail(''); setName(''); setPassword(''); setSystemRole('user');
      setNotice(t('admin.userCreated', { email: created.email }));
      await invalidateProjectUserQueries(queryClient, false);
    }
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    create.mutate();
  }

  function clearFilters() {
    setSearch('');
    setRoleFilter('all');
    setPasswordFilter('all');
  }

  return (
    <section className="panel admin-panel">
      <div className="section-head admin-heading">
        <div><h2>{t('admin.title')}</h2><p>{t('admin.subtitle')}</p></div>
        <span className="admin-heading-icon" aria-hidden="true"><Shield size={22} /></span>
      </div>

      <div className="admin-stats" role="group" aria-label={t('admin.summary')}>
        <StatCard icon={<Users size={20} />} value={allUsers.length} label={t('admin.totalUsers')} />
        <StatCard icon={<ShieldCheck size={20} />} value={administratorCount} label={t('admin.totalAdmins')} />
        <StatCard icon={<Clock3 size={20} />} value={pendingPasswordCount} label={t('admin.pendingPasswords')} warning={pendingPasswordCount > 0} />
      </div>

      <section className="admin-create-card" aria-labelledby="admin-create-title">
        <div className="admin-subheading">
          <div><h3 id="admin-create-title">{t('admin.createTitle')}</h3><p>{t('admin.createHint')}</p></div>
          <UserRound size={20} aria-hidden="true" />
        </div>
        <form className="admin-user-form" onSubmit={submit}>
          <label><span>{t('admin.emailLabel')}</span><input type="email" placeholder={t('admin.emailPlaceholder')} value={email} onChange={(event) => setEmail(event.target.value)} required /></label>
          <label><span>{t('admin.nameLabel')}</span><input maxLength={200} placeholder={t('admin.namePlaceholder')} value={name} onChange={(event) => setName(event.target.value)} required /></label>
          <label><span>{t('admin.passwordLabel')}</span><input type="password" minLength={12} placeholder={t('admin.passwordPlaceholder')} value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
          <label><span>{t('admin.roleLabel')}</span><select value={systemRole} onChange={(event) => setSystemRole(event.target.value as SystemRole)}><option value="user">{t('admin.role.user')}</option><option value="system_admin">{t('admin.role.system_admin')}</option></select></label>
          <button className="primary admin-add-button" disabled={create.isPending}><Plus size={16} /> {create.isPending ? t('admin.adding') : t('admin.add')}</button>
        </form>
        {create.error && <p className="error" role="alert">{errorMessage(create.error, 'admin.createFailed')}</p>}
        {notice && <p className="admin-notice" role="status"><CheckCircle2 size={15} /> {notice}</p>}
      </section>

      <section className="admin-directory" aria-labelledby="admin-directory-title">
        <div className="admin-directory-head">
          <div><h3 id="admin-directory-title">{t('admin.directoryTitle')}</h3><p>{t('admin.directoryHint')}</p></div>
          <span className="admin-result-count">{t('admin.resultCount', { shown: filteredUsers.length, total: allUsers.length })}</span>
        </div>
        <div className="admin-filters">
          <label className="admin-search">
            <Search size={17} aria-hidden="true" />
            <input aria-label={t('admin.searchLabel')} placeholder={t('admin.searchPlaceholder')} value={search} onChange={(event) => setSearch(event.target.value)} />
          </label>
          <select aria-label={t('admin.roleFilterLabel')} value={roleFilter} onChange={(event) => setRoleFilter(event.target.value as RoleFilter)}>
            <option value="all">{t('admin.filter.allRoles')}</option>
            <option value="system_admin">{t('admin.role.system_admin')}</option>
            <option value="user">{t('admin.role.user')}</option>
          </select>
          <select aria-label={t('admin.passwordFilterLabel')} value={passwordFilter} onChange={(event) => setPasswordFilter(event.target.value as PasswordFilter)}>
            <option value="all">{t('admin.filter.allStatuses')}</option>
            <option value="active">{t('admin.status.active')}</option>
            <option value="pending">{t('admin.status.mustChange')}</option>
          </select>
          {hasFilters && <button className="small-btn admin-clear-filters" onClick={clearFilters}><X size={15} /> {t('admin.clearFilters')}</button>}
        </div>

        {users.error && <p className="error" role="alert">{errorMessage(users.error)}</p>}
        {users.isLoading && <div className="empty-state">{t('admin.loading')}</div>}
        {!users.isLoading && !users.error && (
          <div className="admin-user-list">
            {filteredUsers.map((user) => <UserRow key={user.id} user={user} isCurrentUser={user.id === currentUserID} />)}
            {allUsers.length === 0 && <div className="empty-state">{t('admin.empty')}</div>}
            {allUsers.length > 0 && filteredUsers.length === 0 && <div className="empty-state admin-no-results"><Search size={22} /><span>{t('admin.noMatches')}</span><button className="small-btn" onClick={clearFilters}>{t('admin.clearFilters')}</button></div>}
          </div>
        )}
      </section>
    </section>
  );
}

function StatCard({ icon, value, label, warning = false }: { icon: React.ReactNode; value: number; label: string; warning?: boolean }) {
  return (
    <article className={`admin-stat${warning ? ' warning' : ''}`}>
      <span className="admin-stat-icon" aria-hidden="true">{icon}</span>
      <div><strong>{value}</strong><span>{label}</span></div>
    </article>
  );
}

function UserRow({ user, isCurrentUser }: { user: User; isCurrentUser: boolean }) {
  const { errorMessage, formatDateTime, t } = useI18n();
  const queryClient = useQueryClient();
  const [resetting, setResetting] = useState(false);
  const [password, setPassword] = useState('');
  const [editingName, setEditingName] = useState(false);
  const [name, setName] = useState(user.name);
  const [status, setStatus] = useState('');
  const [actionError, setActionError] = useState<unknown>();
  useEffect(() => setName(user.name), [user.name]);
  const trimmedName = name.trim();

  const updateName = useMutation({
    mutationFn: () => api<User>(`/api/admin/users/${encodeURIComponent(user.id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ name: trimmedName })
    }),
    onMutate: () => { setStatus(''); setActionError(undefined); },
    onError: (error) => setActionError(error),
    onSuccess: async (updated) => {
      queryClient.setQueryData<User[]>(usersKey, (current = []) => current.map((candidate) => candidate.id === updated.id ? updated : candidate));
      if (isCurrentUser) queryClient.setQueryData<User>(['auth', 'me'], updated);
      setEditingName(false);
      setStatus(t('admin.nameUpdated'));
      await invalidateProjectUserQueries(queryClient, true);
    }
  });
  const updateRole = useMutation({
    mutationFn: (role: SystemRole) => api<User>(`/api/admin/users/${encodeURIComponent(user.id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ system_role: role })
    }),
    onMutate: () => { setStatus(''); setActionError(undefined); },
    onError: (error) => setActionError(error),
    onSuccess: (updated) => {
      queryClient.setQueryData<User[]>(usersKey, (current = []) => current.map((candidate) => candidate.id === updated.id ? updated : candidate));
      if (isCurrentUser) queryClient.setQueryData<User>(['auth', 'me'], updated);
      setStatus(t('admin.roleUpdated'));
    }
  });
  const reset = useMutation({
    mutationFn: () => api(`/api/admin/users/${encodeURIComponent(user.id)}/password`, {
      method: 'POST',
      body: JSON.stringify({ password })
    }),
    onMutate: () => { setStatus(''); setActionError(undefined); },
    onError: (error) => setActionError(error),
    onSuccess: () => {
      if (isCurrentUser) {
        window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
        return;
      }
      queryClient.setQueryData<User[]>(usersKey, (current = []) => current.map((candidate) => candidate.id === user.id ? { ...candidate, must_change_password: true } : candidate));
      setPassword('');
      setResetting(false);
      setStatus(t('admin.passwordReset'));
    }
  });

  function submitPassword(event: FormEvent) {
    event.preventDefault();
    const confirmation = isCurrentUser ? t('admin.resetOwnPasswordConfirm') : t('admin.resetPasswordConfirm', { email: user.email });
    if (window.confirm(confirmation)) reset.mutate();
  }

  function changeRole(role: SystemRole) {
    if (isCurrentUser && role !== 'system_admin' && !window.confirm(t('admin.demoteOwnRoleConfirm'))) return;
    updateRole.mutate(role);
  }

  function cancelNameEdit() {
    setName(user.name);
    setEditingName(false);
    setActionError(undefined);
  }

  const initials = user.name.trim().split(/\s+/).slice(0, 2).map((part) => part[0]).join('').toLocaleUpperCase() || user.email[0]?.toLocaleUpperCase() || '?';
  return (
    <article className="admin-user-row">
      <div className="admin-user-main">
        <span className="admin-user-avatar" aria-hidden="true">{initials}</span>
        <div className="admin-user-identity">
          {editingName ? (
            <form className="admin-name-form" onSubmit={(event) => { event.preventDefault(); if (trimmedName) updateName.mutate(); }}>
              <input aria-label={t('admin.displayNameFor', { email: user.email })} maxLength={200} value={name} onChange={(event) => setName(event.target.value)} disabled={updateName.isPending} required autoFocus />
              <button className="small-btn" disabled={updateName.isPending || !trimmedName}>{t('common.save')}</button>
              <button type="button" className="small-btn" disabled={updateName.isPending} onClick={cancelNameEdit}>{t('common.cancel')}</button>
            </form>
          ) : (
            <div className="admin-user-name">
              <strong>{user.name}</strong>
              {isCurrentUser && <span className="admin-you-badge">{t('admin.you')}</span>}
              <button className="admin-edit-name" title={t('admin.editName')} aria-label={t('admin.editNameFor', { email: user.email })} onClick={() => { setStatus(''); setActionError(undefined); setEditingName(true); }}><Pencil size={13} /></button>
            </div>
          )}
          <span className="admin-user-email">{user.email}</span>
        </div>
      </div>

      <div className="admin-user-details">
        <span className={`badge admin-role-badge ${user.system_role === 'system_admin' ? 'admin' : ''}`}><Shield size={12} /> {t(systemRoleMessageKeys[user.system_role])}</span>
        <span className={`badge admin-password-badge ${user.must_change_password ? 'pending' : 'active'}`}>
          {user.must_change_password ? <Clock3 size={12} /> : <CheckCircle2 size={12} />}
          {user.must_change_password ? t('admin.status.mustChange') : t('admin.status.active')}
        </span>
        <span className="admin-created"><CalendarDays size={13} /> {t('admin.createdAt', { time: formatDateTime(user.created_at) })}</span>
      </div>

      <div className="admin-user-actions">
        <label className="admin-role-control">
          <span>{t('admin.roleLabel')}</span>
          <select aria-label={t('admin.systemRoleFor', { email: user.email })} value={user.system_role} disabled={updateRole.isPending} onChange={(event) => changeRole(event.target.value as SystemRole)}>
            <option value="user">{t(systemRoleMessageKeys.user)}</option><option value="system_admin">{t(systemRoleMessageKeys.system_admin)}</option>
          </select>
        </label>
        {!resetting && <button className="small-btn admin-reset-button" onClick={() => { setStatus(''); setActionError(undefined); setResetting(true); }}><KeyRound size={15} /> {t('admin.resetPassword')}</button>}
      </div>

      {resetting && (
        <form className="admin-password-reset" onSubmit={submitPassword}>
          <div><strong>{t('admin.resetPasswordFor', { name: user.name })}</strong><span>{t('admin.resetPasswordHint')}</span></div>
          <input aria-label={t('admin.oneTimePasswordFor', { email: user.email })} placeholder={t('admin.passwordPlaceholder')} type="password" minLength={12} value={password} onChange={(event) => setPassword(event.target.value)} disabled={reset.isPending} required autoFocus />
          <button className="primary" disabled={reset.isPending}>{reset.isPending ? t('admin.resettingPassword') : t('admin.confirmReset')}</button>
          <button type="button" className="small-btn" disabled={reset.isPending} onClick={() => { setPassword(''); setResetting(false); setActionError(undefined); }}>{t('common.cancel')}</button>
        </form>
      )}
      {actionError !== undefined && <span className="error admin-row-message" role="alert">{errorMessage(actionError)}</span>}
      {status && <span className="admin-row-message success" role="status"><CheckCircle2 size={14} /> {status}</span>}
    </article>
  );
}

function invalidateProjectUserQueries(queryClient: QueryClient, includeMembers: boolean) {
  return queryClient.invalidateQueries({
    predicate: ({ queryKey }) => queryKey[0] === 'projects'
      && (queryKey[2] === 'member-candidates' || (includeMembers && queryKey[2] === 'members'))
  });
}
