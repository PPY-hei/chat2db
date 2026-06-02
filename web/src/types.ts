export type Role = "owner" | "admin" | "editor" | "viewer";

export interface User {
  id: number;
  email: string;
  name: string;
  created_at: string;
  llm_endpoint?: string;
  llm_model?: string;
}

export interface MeResponse {
  user: User;
  llm_configured: boolean;
}

export interface Group {
  id: number;
  name: string;
  description: string;
  owner_id: number;
  share_llm: boolean;
  created_at: string;
  updated_at: string;
  role: Role;
  member_count: number;
}

export interface Connection {
  id: number;
  group_id: number;
  name: string;
  driver: string;
  host: string;
  port: number;
  database: string;
  username: string;
  ssl_mode: string;
  // SSH 隧道
  ssh_enabled: boolean;
  ssh_host: string;
  ssh_port: number;
  ssh_user: string;
  ssh_auth_method: string;
  // HTTP/SOCKS5 代理
  proxy_enabled: boolean;
  proxy_type: string;
  proxy_host: string;
  proxy_port: number;
  proxy_username: string;
  created_by_id: number;
  created_at: string;
  updated_at: string;
}

export interface Member {
  user_id: number;
  email: string;
  name: string;
  role: Role;
}

export interface SchemaInfo {
  name: string;
}

export interface TableInfo {
  schema: string;
  name: string;
  kind: string;
}

export interface ColumnInfo {
  name: string;
  data_type: string;
  nullable: boolean;
  default_value?: string | null;
  is_primary: boolean;
  comment?: string;
  auto_increment?: boolean;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  unique: boolean;
  primary: boolean;
  method?: string;
}

export interface QueryResult {
  columns?: string[];
  rows?: any[][];
  rows_affected: number;
  truncated: boolean;
  elapsed_ms: number;
  types?: string[];
  tag?: string;
  message?: string;
}

export interface ExecuteResponse {
  results: QueryResult[];
  role?: Role;
  error?: string;
  failed_sql?: string;
}

// DriverInfo 对应后端 dbexec.DriverInfo（GET /api/drivers）。
// 字段与 Capabilities embedded JSON 一一对应，新增字段不要 breaking 旧字段。
export interface DriverInfo {
  name: string;
  default_port: number;
  ssl_modes: string[];
  supports_ssh: boolean;
  supports_proxy: boolean;
  supports_mtls: boolean;
  schema_support: boolean;
}

export interface SavedQuery {
  id: number;
  group_id: number;
  connection_id: number;
  title: string;
  description: string;
  sql: string;
  created_by_id: number;
  group_name?: string;
  connection_name?: string;
  database?: string;
  created_by_name?: string;
}

export type AuditAction =
  | "sql.execute"
  | "auth.login.success"
  | "auth.login.fail"
  | "auth.register"
  | "member.add"
  | "member.remove"
  | "member.update"
  | "connection.create"
  | "connection.update"
  | "connection.delete"
  | "connection.test";

export interface AuditLog {
  id: number;
  created_at: string;
  user_id?: number;
  user_email: string;
  action: AuditAction;
  group_id?: number;
  conn_id?: number;
  target: string;
  detail: string;
  success: boolean;
  duration_ms: number;
  error_msg: string;
  ip: string;
  user_agent: string;
}

export interface AuditLogQuery {
  from?: string;
  to?: string;
  actions?: AuditAction[];
  keyword?: string;
  only_fail?: boolean;
  page?: number;
  size?: number;
  group_id?: number;
}

export interface AuditLogPage {
  items: AuditLog[];
  total: number;
  page: number;
  size: number;
  visible_group_ids: number[];
}

// ========== 异步任务（导入 / 导出 / 同步） ==========

export type TaskKind = "export" | "import" | "schema_sync" | "data_sync";
export type TaskScope = "connection" | "database" | "schema" | "table";
export type TaskStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled";

export interface Task {
  id: number;
  group_id: number;
  conn_id: number;
  target_conn_id: number; // 目标连接（仅同步任务使用）
  kind: TaskKind;
  scope: TaskScope;
  target_database: string;
  target_schema: string;
  target_table: string;
  dest_database: string; // 目标数据库（仅同步任务使用）
  dest_schema: string; // 目标 schema（仅同步任务使用）
  dest_table: string; // 目标表（仅同步任务使用）
  status: TaskStatus;
  progress: number;
  processed_rows: number;
  total_rows: number;
  total_tables: number;
  done_tables: number;
  file_path: string;
  file_size: number;
  error_msg: string;
  cancel_requested: boolean;
  created_by_id: number;
  creator_name: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
}

export interface TaskQuery {
  group_id?: number;
  conn_id?: number;
  kind?: TaskKind;
  scope?: TaskScope;
  status?: TaskStatus;
  keyword?: string;
  from?: string;
  to?: string;
  page?: number;
  size?: number;
}

export interface TaskPage {
  items: Task[];
  total: number;
  page: number;
  size: number;
  visible_group_ids: number[];
}

export interface CreateTaskRequest {
  group_id: number;
  conn_id: number;
  target_conn_id?: number; // 目标连接（仅同步任务使用）
  kind: TaskKind;
  scope: TaskScope;
  target_database?: string;
  target_schema?: string;
  target_table?: string;
  dest_database?: string; // 目标数据库（仅同步任务使用）
  dest_schema?: string; // 目标 schema（仅同步任务使用）
  dest_table?: string; // 目标表（仅同步任务使用）
}
