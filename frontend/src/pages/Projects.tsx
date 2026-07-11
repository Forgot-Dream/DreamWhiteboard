import { FormEvent, useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderKanban, LayoutGrid, Pencil, Plus, Trash2, Users } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { api, canEdit, canManage, canManageOwners, type Board, type Project, type ProjectMember, type ProjectRole, type User } from '../lib/api';
import { useI18n } from '../lib/i18n';

const projectsKey = ['projects'] as const;
const projectKey = (id: string) => ['projects', id] as const;
const boardsKey = (id: string) => ['projects', id, 'boards'] as const;
const membersKey = (id: string) => ['projects', id, 'members'] as const;

export function Projects({ user }: { user: User }) {
  const { projectId = '' } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { formatDateTime } = useI18n();
  const [projectName, setProjectName] = useState('');
  const [projectDescription, setProjectDescription] = useState('');
  const [boardName, setBoardName] = useState('');
  const [memberIdentity, setMemberIdentity] = useState('');
  const [memberRole, setMemberRole] = useState<ProjectRole>('editor');

  const projects = useQuery({ queryKey: projectsKey, queryFn: () => api<Project[]>('/api/projects') });
  const selected = useQuery({
    queryKey: projectKey(projectId),
    queryFn: () => api<Project>(`/api/projects/${encodeURIComponent(projectId)}`),
    enabled: Boolean(projectId)
  });
  const boards = useQuery({
    queryKey: boardsKey(projectId),
    queryFn: () => api<Board[]>(`/api/projects/${encodeURIComponent(projectId)}/boards`),
    enabled: Boolean(projectId)
  });
  const members = useQuery({
    queryKey: membersKey(projectId),
    queryFn: () => api<ProjectMember[]>(`/api/projects/${encodeURIComponent(projectId)}/members`),
    enabled: Boolean(projectId)
  });
  const users = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: () => api<User[]>('/api/admin/users'),
    enabled: user.system_role === 'system_admin'
  });

  useEffect(() => {
    if (!projectId && projects.data?.[0]) navigate(`/projects/${projects.data[0].id}`, { replace: true });
  }, [navigate, projectId, projects.data]);

  const myRole = useMemo(
    () => members.data?.find((member) => member.user_id === user.id)?.role,
    [members.data, user.id]
  );
  const manageable = canManage(myRole, user);
  const editable = canEdit(myRole, user);

  const createProject = useMutation({
    mutationFn: () => api<Project>('/api/projects', { method: 'POST', body: JSON.stringify({ name: projectName, description: projectDescription }) }),
    onSuccess: async (project) => {
      setProjectName(''); setProjectDescription('');
      await queryClient.invalidateQueries({ queryKey: projectsKey });
      navigate(`/projects/${project.id}`);
    }
  });
  const updateProject = useMutation({
    mutationFn: (input: { name: string; description: string }) => api<Project>(`/api/projects/${encodeURIComponent(projectId)}`, { method: 'PATCH', body: JSON.stringify(input) }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: projectsKey }),
        queryClient.invalidateQueries({ queryKey: projectKey(projectId) })
      ]);
    }
  });
  const deleteProject = useMutation({
    mutationFn: () => api(`/api/projects/${encodeURIComponent(projectId)}`, { method: 'DELETE' }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: projectsKey });
      navigate('/projects', { replace: true });
    }
  });
  const createBoard = useMutation({
    mutationFn: () => api<Board>(`/api/projects/${encodeURIComponent(projectId)}/boards`, { method: 'POST', body: JSON.stringify({ name: boardName }) }),
    onSuccess: async () => { setBoardName(''); await queryClient.invalidateQueries({ queryKey: boardsKey(projectId) }); }
  });
  const addMember = useMutation({
    mutationFn: () => {
      const selectedUser = users.data?.find((candidate) => candidate.id === memberIdentity || candidate.email === memberIdentity);
      return api<ProjectMember>(`/api/projects/${encodeURIComponent(projectId)}/members`, {
        method: 'POST',
        body: JSON.stringify({ user_id: selectedUser?.id ?? memberIdentity, role: memberRole })
      });
    },
    onSuccess: async () => { setMemberIdentity(''); await queryClient.invalidateQueries({ queryKey: membersKey(projectId) }); }
  });

  const error = projects.error ?? selected.error ?? boards.error ?? members.error ?? createProject.error ?? updateProject.error ?? deleteProject.error ?? createBoard.error ?? addMember.error;

  return (
    <div className="workspace-grid">
      <aside className="sidebar panel">
        <div className="section-head"><h2>Projects</h2><FolderKanban size={20} /></div>
        <form className="stack-form" onSubmit={(event) => { event.preventDefault(); createProject.mutate(); }}>
          <input placeholder="Project name" value={projectName} onChange={(event) => setProjectName(event.target.value)} required />
          <textarea placeholder="Description (optional)" value={projectDescription} onChange={(event) => setProjectDescription(event.target.value)} />
          <button className="primary" disabled={createProject.isPending}><Plus size={16} /> Create</button>
        </form>
        <div className="nav-list">
          {projects.data?.map((project) => <button className={project.id === projectId ? 'active' : ''} key={project.id} onClick={() => navigate(`/projects/${project.id}`)}>{project.name}</button>)}
          {!projects.isLoading && projects.data?.length === 0 && <div className="empty-state">Create your first project.</div>}
        </div>
      </aside>

      <section className="panel boards-panel">
        <div className="section-head">
          <div><h2>{selected.data?.name ?? 'Boards'}</h2><p>{selected.data?.description || 'Create and open project whiteboards.'}</p></div>
          <LayoutGrid size={22} />
        </div>
        {manageable && selected.data && <ProjectSettings project={selected.data} onSave={(value) => updateProject.mutate(value)} onDelete={() => {
          if (window.confirm(`Delete “${selected.data?.name}” and all of its boards and uploads?`)) deleteProject.mutate();
        }} />}
        <form className="inline-form board-create-form" onSubmit={(event) => { event.preventDefault(); createBoard.mutate(); }}>
          <input placeholder="Board name" value={boardName} onChange={(event) => setBoardName(event.target.value)} disabled={!projectId || !editable} required />
          <button className="primary" disabled={!projectId || !editable || createBoard.isPending}><Plus size={16} /> Board</button>
        </form>
        {error && <p className="error" role="alert">{error.message}</p>}
        <div className="card-grid">
          {boards.data?.map((board) => <BoardCard key={board.id} board={board} projectID={projectId} editable={editable} manageable={manageable} formatDateTime={formatDateTime} />)}
          {!boards.isLoading && boards.data?.length === 0 && projectId && <div className="empty-state">No boards yet.</div>}
        </div>
      </section>

      <section className="panel members-panel">
        <div className="section-head"><h2>Members</h2><Users size={20} /></div>
        {manageable && <form className="inline-form member-form" onSubmit={(event) => { event.preventDefault(); addMember.mutate(); }}>
          {users.data ? (
            <select value={memberIdentity} onChange={(event) => setMemberIdentity(event.target.value)} required><option value="">Select user</option>{users.data.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.email}</option>)}</select>
          ) : <input placeholder="User ID" value={memberIdentity} onChange={(event) => setMemberIdentity(event.target.value)} required />}
          <select value={memberRole} onChange={(event) => setMemberRole(event.target.value as ProjectRole)}>
            {canManageOwners(myRole, user) && <option value="owner">owner</option>}<option value="admin">admin</option><option value="editor">editor</option><option value="viewer">viewer</option>
          </select>
          <button className="primary" disabled={addMember.isPending}><Plus size={16} /> Add</button>
        </form>}
        <div className="table compact">
          {members.data?.map((member) => <MemberRow key={member.user_id} member={member} projectID={projectId} canManage={manageable} canManageOwners={canManageOwners(myRole, user)} />)}
          {!members.isLoading && members.data?.length === 0 && <div className="empty-state">No members.</div>}
        </div>
      </section>
    </div>
  );
}

function ProjectSettings({ project, onSave, onDelete }: { project: Project; onSave: (value: { name: string; description: string }) => void; onDelete: () => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  useEffect(() => { setName(project.name); setDescription(project.description); }, [project]);
  if (!open) return <button className="small-btn project-edit-button" onClick={() => setOpen(true)}><Pencil size={14} /> Edit project</button>;
  return (
    <form className="project-settings" onSubmit={(event) => { event.preventDefault(); onSave({ name, description }); setOpen(false); }}>
      <input value={name} onChange={(event) => setName(event.target.value)} required />
      <textarea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Description" />
      <div><button className="primary">Save</button><button type="button" className="small-btn" onClick={() => setOpen(false)}>Cancel</button><button type="button" className="small-btn danger" onClick={onDelete}><Trash2 size={14} /> Delete</button></div>
    </form>
  );
}

function BoardCard({ board, projectID, editable, manageable, formatDateTime }: { board: Board; projectID: string; editable: boolean; manageable: boolean; formatDateTime: (value: string | Date) => string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(board.name);
  const rename = useMutation({
    mutationFn: () => api<Board>(`/api/boards/${encodeURIComponent(board.id)}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
    onSuccess: async () => { setEditing(false); await queryClient.invalidateQueries({ queryKey: boardsKey(projectID) }); }
  });
  const remove = useMutation({
    mutationFn: () => api(`/api/boards/${encodeURIComponent(board.id)}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: boardsKey(projectID) })
  });
  return (
    <article className="board-card">
      {editing ? <form onSubmit={(event) => { event.preventDefault(); rename.mutate(); }}><input value={name} onChange={(event) => setName(event.target.value)} autoFocus required /><button className="small-btn">Save</button><button type="button" className="small-btn" onClick={() => setEditing(false)}>Cancel</button></form> : <button className="board-open" onClick={() => navigate(`/boards/${board.id}`)}><strong>{board.name}</strong><span>Updated {formatDateTime(board.updated_at)}</span></button>}
      {(editable || manageable) && !editing && <div className="card-actions">{editable && <button title="Rename" onClick={() => setEditing(true)}><Pencil size={14} /></button>}{manageable && <button className="danger" title="Delete" onClick={() => window.confirm(`Delete “${board.name}”?`) && remove.mutate()}><Trash2 size={14} /></button>}</div>}
      {(rename.error || remove.error) && <span className="error">{rename.error?.message ?? remove.error?.message}</span>}
    </article>
  );
}

function MemberRow({ member, projectID, canManage: manageable, canManageOwners: owners }: { member: ProjectMember; projectID: string; canManage: boolean; canManageOwners: boolean }) {
  const queryClient = useQueryClient();
  const update = useMutation({
    mutationFn: (role: ProjectRole) => api<ProjectMember>(`/api/projects/${encodeURIComponent(projectID)}/members`, { method: 'POST', body: JSON.stringify({ user_id: member.user_id, role }) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: membersKey(projectID) })
  });
  const remove = useMutation({
    mutationFn: () => api(`/api/projects/${encodeURIComponent(projectID)}/members?user_id=${encodeURIComponent(member.user_id)}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: membersKey(projectID) })
  });
  return (
    <div className="table-row member-row">
      <span>{member.user?.email ?? member.user_id}</span>
      {manageable ? <select value={member.role} disabled={member.role === 'owner' && !owners} onChange={(event) => update.mutate(event.target.value as ProjectRole)}>{owners && <option value="owner">owner</option>}<option value="admin">admin</option><option value="editor">editor</option><option value="viewer">viewer</option></select> : <span className="badge">{member.role}</span>}
      {manageable && <button className="small-btn danger" title="Remove member" disabled={member.role === 'owner' && !owners} onClick={() => window.confirm('Remove this member?') && remove.mutate()}><Trash2 size={14} /></button>}
      {(update.error || remove.error) && <span className="error row-error">{update.error?.message ?? remove.error?.message}</span>}
    </div>
  );
}
