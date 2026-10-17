// 项目切换弹层的纯过滤助手：只对已加载列表本地过滤，不发 API。
export interface ProjectLike {
  name: string
  status: string
}

export function filterProjects<T extends ProjectLike>(projects: T[], query: string): T[] {
  const q = query.trim().toLowerCase()
  if (!q) return projects
  return projects.filter(p => p.name.toLowerCase().includes(q) || p.status.toLowerCase().includes(q))
}
