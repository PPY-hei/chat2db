import axios, { AxiosInstance } from "axios";
import type {
  Connection,
  ColumnInfo,
  ExecuteResponse,
  Group,
  Member,
  MeResponse,
  SavedQuery,
  SchemaInfo,
  TableInfo,
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
  listSchemas: (connID: number) =>
    http.get<SchemaInfo[]>(`/connections/${connID}/schemas`).then((r) => r.data),
  listTables: (connID: number, schema: string) =>
    http.get<TableInfo[]>(`/connections/${connID}/tables`, { params: { schema } }).then((r) => r.data),
  listColumns: (connID: number, schema: string, table: string) =>
    http
      .get<ColumnInfo[]>(`/connections/${connID}/columns`, { params: { schema, table } })
      .then((r) => r.data),
  getTableDDL: (connID: number, schema: string, table: string) =>
    http
      .get<{ schema: string; table: string; ddl: string }>(`/connections/${connID}/ddl`, {
        params: { schema, table },
      })
      .then((r) => r.data),
  execute: (connID: number, sql: string) =>
    http.post<ExecuteResponse>(`/connections/${connID}/execute`, { sql }).then((r) => r.data),

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
};
