import axios, { AxiosInstance } from "axios";
import type {
  AuditAction,
  AuditLogPage,
  AuditLogQuery,
  Connection,
  ColumnInfo,
  CreateTaskRequest,
  DriverInfo,
  ExecuteResponse,
  Group,
  Member,
  MeResponse,
  SavedQuery,
  SchemaInfo,
  TableInfo,
  Task,
  TaskPage,
  TaskQuery,
} from "./types";

const TOKEN_KEY = "chat2db.token";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(t: string | null) {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
}

const http: AxiosInstance = axios.create({
  baseURL: "/api",
  timeout: 60_000,
});

http.interceptors.request.use((c) => {
  const t = getToken();
  if (t) c.headers.Authorization = `Bearer ${t}`;
  return c;
});

http.interceptors.response.use(
  (r) => r,
  (err) => {
    // 后端 4xx/5xx 响应体里都附了 request_id（middleware/RequestID + handlers.errorBody）；
    // 兜底从响应头 X-Request-ID 取，覆盖纯 panic / 网关返回等场景。
    // 仅挂到 err 对象，UI 展示留给后续 ErrorBoundary 迭代。
    const requestId =
      err.response?.data?.request_id ||
      err.response?.headers?.["x-request-id"];
    if (requestId) {
      err.requestId = requestId;
    }

    if (err.response?.status === 401) {
      setToken(null);
      if (!location.pathname.startsWith("/login")) {
        location.href = "/login";
      }
    }
    return Promise.reject(err);
  }
);

export const api = {
  // drivers（公开接口，未登录时也可用，供连接表单动态渲染）
  listDrivers: () =>
    http.get<{ drivers: DriverInfo[] }>("/drivers").then((r) => r.data.drivers),

  // auth
  register: (email: string, name: string, password: string) =>
    http.post<{ token: string }>("/auth/register", { email, name, password }).then((r) => r.data),
  login: (email: string, password: string) =>
    http.post<{ token: string }>("/auth/login", { email, password }).then((r) => r.data),
  me: () => http.get<MeResponse>("/me").then((r) => r.data),
  updateLLM: (endpoint: string, model: string, apiKey: string) =>
    http.put("/me/llm", { endpoint, model, api_key: apiKey }).then((r) => r.data),

  // groups
  listGroups: () => http.get<Group[]>("/groups").then((r) => r.data),
  createGroup: (name: string, description: string) =>
    http.post<Group>("/groups", { name, description }).then((r) => r.data),
  updateGroup: (
    groupID: number,
    patch: { name?: string; description?: string; share_llm?: boolean }
  ) => http.put<Group>(`/groups/${groupID}`, patch).then((r) => r.data),
  listMembers: (groupID: number) =>
    http.get<Member[]>(`/groups/${groupID}/members`).then((r) => r.data),
  addMember: (groupID: number, email: string, role: string) =>
    http.post(`/groups/${groupID}/members`, { email, role }).then((r) => r.data),
  removeMember: (groupID: number, userID: number) =>
    http.delete(`/groups/${groupID}/members/${userID}`).then((r) => r.data),

  // connections
  listConnections: (groupID: number) =>
    http.get<Connection[]>(`/groups/${groupID}/connections`).then((r) => r.data),
  createConnection: (groupID: number, body: any) =>
    http.post<Connection>(`/groups/${groupID}/connections`, body).then((r) => r.data),
  updateConnection: (connID: number, body: any) =>
    http.put<Connection>(`/connections/${connID}`, body).then((r) => r.data),
  deleteConnection: (connID: number) =>
    http.delete(`/connections/${connID}`).then((r) => r.data),
  testConnection: (payload: { connection_id?: number; draft?: any }) =>
    http.post<{ ok: boolean; error?: string }>("/connections/test", payload).then((r) => r.data),

  // db browsing & execution
  listDatabases: (connID: number) =>
    http
      .get<Array<{ name: string; owner: string; current: boolean }>>(`/connections/${connID}/databases`)
      .then((r) => r.data),
  listSchemas: (connID: number, database?: string) =>
    http
      .get<SchemaInfo[]>(`/connections/${connID}/schemas`, {
        params: database ? { database } : {},
      })
      .then((r) => r.data),
  listTables: (connID: number, schema: string, database?: string) =>
    http
      .get<TableInfo[]>(`/connections/${connID}/tables`, {
        params: { schema, ...(database ? { database } : {}) },
      })
      .then((r) => r.data),
  listColumns: (connID: number, schema: string, table: string, database?: string) =>
    http
      .get<ColumnInfo[]>(`/connections/${connID}/columns`, {
        params: { schema, table, ...(database ? { database } : {}) },
      })
      .then((r) => r.data),
  getTableDDL: (connID: number, schema: string, table: string, database?: string) =>
    http
      .get<{ schema: string; table: string; ddl: string }>(`/connections/${connID}/ddl`, {
        params: { schema, table, ...(database ? { database } : {}) },
      })
      .then((r) => r.data),
  execute: (connID: number, sql: string, database?: string, signal?: AbortSignal) =>
    http
      .post<ExecuteResponse>(
        `/connections/${connID}/execute${database ? `?database=${encodeURIComponent(database)}` : ""}`,
        { sql },
        { signal }
      )
      .then((r) => r.data),

  // saved queries
  listGroupSavedQueries: (groupID: number) =>
    http.get<SavedQuery[]>(`/groups/${groupID}/saved-queries`).then((r) => r.data),
  listMySavedQueries: () => http.get<SavedQuery[]>("/me/saved-queries").then((r) => r.data),
  createSavedQuery: (body: {
    connection_id: number;
    title: string;
    description?: string;
    sql: string;
  }) => http.post<SavedQuery>("/saved-queries", body).then((r) => r.data),
  deleteSavedQuery: (id: number) => http.delete(`/saved-queries/${id}`).then((r) => r.data),

  // ai
  aiChat: (body: {
    prompt: string;
    dialect?: string;
    selection?: string;
    table_ddl?: string;
  }) =>
    http
      .post<{ sql: string; explanation?: string; raw: string }>("/ai/chat", body)
      .then((r) => r.data),

  // 审计日志（仅在任一组是 admin/owner 的用户可读，由后端按组隔离）
  listAuditLogs: (q: AuditLogQuery = {}) =>
    http
      .get<AuditLogPage>("/audit/logs", {
        params: {
          ...q,
          actions: q.actions?.join(","),
          only_fail: q.only_fail ? 1 : undefined,
        },
      })
      .then((r) => r.data),
  listAuditActions: () =>
    http.get<{ actions: AuditAction[] }>("/audit/actions").then((r) => r.data.actions),

  // 异步任务（导入 / 导出）
  listTasks: (q: TaskQuery = {}) =>
    http
      .get<TaskPage>("/tasks", { params: { ...q } })
      .then((r) => r.data),
  createTask: (body: CreateTaskRequest) =>
    http.post<Task>("/tasks", body).then((r) => r.data),
  getTask: (id: number) => http.get<Task>(`/tasks/${id}`).then((r) => r.data),
  cancelTask: (id: number) =>
    http.post(`/tasks/${id}/cancel`).then((r) => r.data),
  deleteTask: (id: number) =>
    http.delete(`/tasks/${id}`).then((r) => r.data),
  downloadTaskUrl: (id: number) => `/api/tasks/${id}/download`,
};
