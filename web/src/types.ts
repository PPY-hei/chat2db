export type Role = "owner" | "editor" | "viewer";

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
