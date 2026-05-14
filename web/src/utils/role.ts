import type { Role } from "../types";

// 角色层级：owner > admin > editor > viewer
// 与后端 service/group.go 的 roleAtLeast 保持一致。
const RANK: Record<Role, number> = {
  viewer: 1,
  editor: 2,
  admin: 3,
  owner: 4,
};

export function roleAtLeast(have: Role | undefined, want: Role): boolean {
  if (!have) return false;
  return RANK[have] >= RANK[want];
}

// canWrite: 能否执行 INSERT/UPDATE/DELETE 等 DML，用于"编辑数据"入口。
export function canWrite(role: Role | undefined): boolean {
  return roleAtLeast(role, "editor");
}

// canDDL: 能否执行 CREATE/ALTER/DROP/TRUNCATE 等 DDL，用于"建表/改字段/重建表"入口。
export function canDDL(role: Role | undefined): boolean {
  return roleAtLeast(role, "admin");
}

// canManageGroup: 能否管理组（邀请/踢人、改组信息、共享 LLM、连接 CRUD）。
export function canManageGroup(role: Role | undefined): boolean {
  return role === "owner";
}

// canInviteMember: 能否邀请新成员（仅 viewer/editor），Admin 与 Editor 都有资格邀请。
export function canInviteMember(role: Role | undefined): boolean {
  return roleAtLeast(role, "editor");
}

// Tag 配色；新增 admin 用蓝色以区别 owner(orange) / editor(green)。
export const ROLE_TAG_COLOR: Record<Role, string> = {
  owner: "orange",
  admin: "blue",
  editor: "green",
  viewer: "default",
};
