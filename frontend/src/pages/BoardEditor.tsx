import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate, useParams } from 'react-router-dom';
import { api, canEdit as canUserEdit, type BoardAccess, type Project, type ProjectMember, type ProjectRole, type User } from '../lib/api';
import { BoardToolbar } from '../board/BoardToolbar';
import { CanvasViewport } from '../board/CanvasViewport';
import { useBoardKeyboard } from '../board/keyboard';
import { useBoardRuntime } from '../board/runtime';

export function BoardEditor({ user }: { user: User }) {
  const { boardId = '' } = useParams();
  const navigate = useNavigate();
  const access = useQuery({
    queryKey: ['boards', boardId],
    queryFn: () => api<BoardAccess>(`/api/boards/${encodeURIComponent(boardId)}`),
    enabled: Boolean(boardId),
    retry: 1
  });
  const projectID = access.data?.board.project_id ?? '';
  const project = useQuery({
    queryKey: ['projects', projectID],
    queryFn: () => api<Project>(`/api/projects/${encodeURIComponent(projectID)}`),
    enabled: Boolean(projectID) && !access.data?.project
  });
  const members = useQuery({
    queryKey: ['projects', projectID, 'members'],
    queryFn: () => api<ProjectMember[]>(`/api/projects/${encodeURIComponent(projectID)}/members`),
    enabled: Boolean(projectID) && access.data?.permission?.role === undefined && user.system_role !== 'system_admin'
  });
  const role = access.data?.permission?.role ?? members.data?.find((member) => member.user_id === user.id)?.role;

  if (access.isLoading) return <div className="loading">Loading board…</div>;
  if (access.error || !access.data) return <div className="loading error-page"><div><h2>Unable to open board</h2><p>{access.error?.message ?? 'Board not found'}</p><button className="primary" onClick={() => navigate('/projects')}>Back to projects</button></div></div>;

  return (
    <BoardWorkspace
      key={boardId}
      access={access.data}
      project={access.data.project ?? project.data}
      role={role}
      user={user}
    />
  );
}

function BoardWorkspace({ access, project, role, user }: { access: BoardAccess; project?: Project; role?: ProjectRole; user: User }) {
  const editable = useMemo(() => canUserEdit(role, user, access.permission?.can_edit), [access.permission?.can_edit, role, user]);
  const runtime = useBoardRuntime(access.board.id, user, editable);
  useBoardKeyboard(runtime);
  return (
    <div className="editor-shell">
      <BoardToolbar runtime={runtime} board={access.board} project={project} onBackProjectID={access.board.project_id} />
      <CanvasViewport runtime={runtime} />
    </div>
  );
}
