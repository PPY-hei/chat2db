import { useEffect, useMemo, useState } from "react";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { App, Button, Checkbox, Form, Input, Modal, Radio, Select, Typography } from "antd";
import { api } from "../api";
import type {
  ColumnInfo,
  Connection,
  ExportReplacementOnMissing,
  ExportValueReplacement,
  Group,
  TaskScope,
} from "../types";
import { canDDL } from "../utils/role";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onCreated: () => void;
}

type ExportFormat = "csv" | "insert_sql";

const MAX_WHERE_CONDITION_LENGTH = 200000;
const SCHEMA_SELECT_WIDTH = 120;
const TABLE_SELECT_MIN_WIDTH = 220;
const TABLE_SELECT_MAX_WIDTH = 520;
const TABLE_NAME_CHAR_WIDTH = 8;

interface ValueReplacementDraft {
  id: number;
  column?: string;
  mappingText: string;
  onMissing: ExportReplacementOnMissing;
}

const createValueReplacementDraft = (): ValueReplacementDraft => ({
  id: Date.now() + Math.floor(Math.random() * 100000),
  mappingText: "{\n  \"old_value\": \"new_value\"\n}",
  onMissing: "keep",
});

/**
 * TaskCreateModal —— 创建导出任务。
 *
 * 联动顺序：组 → 连接 → 范围（整连接 / 整库 / 单表）→ 必要的库 / schema / 表
 *
 * 设计取舍：
 *   - 仅 admin/owner/editor 可创建（后端 RequireRole editor，前端 UI 同步过滤组）；
 *   - 当前版本仅支持 export，import 留待后续扩展；
 *   - 选库 / 表的下拉数据按需 lazy 调用现有元数据接口。
 */
export default function TaskCreateModal({ open, groups, onClose, onCreated }: Props) {
  const { message } = App.useApp();
  const [submitting, setSubmitting] = useState(false);

  const [groupID, setGroupID] = useState<number | undefined>();
  const [connections, setConnections] = useState<Connection[]>([]);
  const [connID, setConnID] = useState<number | undefined>();
  const [scope, setScope] = useState<TaskScope>("table");
  const [databases, setDatabases] = useState<string[]>([]);
  const [database, setDatabase] = useState<string | undefined>();
  const [schemas, setSchemas] = useState<string[]>([]);
  const [schema, setSchema] = useState<string | undefined>();
  const [tables, setTables] = useState<string[]>([]);
  const [table, setTable] = useState<string | undefined>();
  const [columns, setColumns] = useState<ColumnInfo[]>([]);
  const [exportFormat, setExportFormat] = useState<ExportFormat>("csv");
  const [whereCondition, setWhereCondition] = useState("");
  const [onConflictDoNothing, setOnConflictDoNothing] = useState(false);
  const [valueReplacements, setValueReplacements] = useState<ValueReplacementDraft[]>([]);

  // 仅展示当前用户至少为 editor 的组（owner/admin/editor）。
  // editor 在 utils/role 没有专门 helper，这里用 RANK 间接判断：admin/owner/editor 都至少能 write。
  const eligibleGroups = useMemo(
    () => groups.filter((g) => g.role === "editor" || canDDL(g.role)),
    [groups]
  );

  useEffect(() => {
    if (!open) {
      // 关闭时重置
      setGroupID(undefined);
      setConnections([]);
      setConnID(undefined);
      setScope("table");
      setDatabases([]);
      setDatabase(undefined);
      setSchemas([]);
      setSchema(undefined);
      setTables([]);
      setTable(undefined);
      setColumns([]);
      setExportFormat("csv");
      setWhereCondition("");
      setOnConflictDoNothing(false);
      setValueReplacements([]);
    }
  }, [open]);

  const selectedConnection = useMemo(
    () => connections.find((c) => c.id === connID),
    [connections, connID]
  );

  const tableSelectWidth = useMemo(() => {
    const longest = Math.max(
      table?.length ?? 0,
      ...tables.map((name) => name.length),
      "选择表".length
    );
    return Math.min(
      TABLE_SELECT_MAX_WIDTH,
      Math.max(TABLE_SELECT_MIN_WIDTH, longest * TABLE_NAME_CHAR_WIDTH + 64)
    );
  }, [table, tables]);

  useEffect(() => {
    if (!groupID) {
      setConnections([]);
      setConnID(undefined);
      return;
    }
    api
      .listConnections(groupID)
      .then(setConnections)
      .catch((e) => message.error(e?.response?.data?.error ?? "加载连接失败"));
  }, [groupID, message]);

  useEffect(() => {
    if (!connID) {
      setDatabases([]);
      setDatabase(undefined);
      setColumns([]);
      setValueReplacements([]);
      return;
    }
    api
      .listDatabases(connID)
      .then((dbs) => setDatabases(dbs.map((d) => d.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载数据库失败"));
  }, [connID, message]);

  useEffect(() => {
    if (!connID || !database || scope !== "table") {
      setSchemas([]);
      setSchema(undefined);
      setColumns([]);
      setValueReplacements([]);
      return;
    }
    api
      .listSchemas(connID, database)
      .then((s) => {
        const names = s.map((x) => x.name);
        setSchemas(names);
        // MySQL: schema=database，自动选第一个
        if (names.length === 1) setSchema(names[0]);
      })
      .catch((e) => message.error(e?.response?.data?.error ?? "加载 schema 失败"));
  }, [connID, database, scope, message]);

  useEffect(() => {
    if (!connID || !database || !schema || scope !== "table") {
      setTables([]);
      setTable(undefined);
      setColumns([]);
      setValueReplacements([]);
      return;
    }
    api
      .listTables(connID, schema, database)
      .then((t) => setTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载表失败"));
  }, [connID, database, schema, scope, message]);

  useEffect(() => {
    if (!connID || !database || !schema || !table || scope !== "table") {
      setColumns([]);
      return;
    }
    api
      .listColumns(connID, schema, table, database)
      .then(setColumns)
      .catch((e) => message.error(e?.response?.data?.error ?? "加载列失败"));
  }, [connID, database, schema, table, scope, message]);

  const updateValueReplacement = (id: number, patch: Partial<ValueReplacementDraft>) => {
    setValueReplacements((items) =>
      items.map((item) => (item.id === id ? { ...item, ...patch } : item))
    );
  };

  const parseValueReplacements = (): ExportValueReplacement[] | undefined => {
    if (scope !== "table" || exportFormat !== "insert_sql" || valueReplacements.length === 0) {
      return undefined;
    }

    const seenColumns = new Set<string>();
    const parsed: ExportValueReplacement[] = [];
    for (const [index, item] of valueReplacements.entries()) {
      const column = item.column?.trim();
      if (!column) {
        throw new Error(`第 ${index + 1} 个字段替换规则未选择列`);
      }
      if (seenColumns.has(column)) {
        throw new Error(`字段 ${column} 的替换规则重复`);
      }
      seenColumns.add(column);

      let raw: unknown;
      try {
        raw = JSON.parse(item.mappingText);
      } catch {
        throw new Error(`字段 ${column} 的替换 JSON 格式不正确`);
      }
      if (!raw || Array.isArray(raw) || typeof raw !== "object") {
        throw new Error(`字段 ${column} 的替换 JSON 必须是对象`);
      }

      const mapping: Record<string, string> = {};
      for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
        if (Array.isArray(value) || (value !== null && typeof value === "object")) {
          throw new Error(`字段 ${column} 的替换目标值必须是字符串、数字、布尔值或 null`);
        }
        mapping[key] = value === null ? "" : String(value);
      }
      parsed.push({
        column,
        mapping,
        on_missing: item.onMissing,
      });
    }

    return parsed;
  };

  const submit = async () => {
    if (!groupID || !connID) {
      message.warning("请选择连接组与连接");
      return;
    }
    if (scope === "database" && !database) {
      message.warning("请选择目标数据库");
      return;
    }
    if (scope === "table" && (!database || !schema || !table)) {
      message.warning("请选择目标库 / schema / 表");
      return;
    }
    if (scope !== "table" && (exportFormat !== "csv" || whereCondition.trim())) {
      message.warning("筛选条件和 INSERT SQL 仅支持单表导出");
      return;
    }
    if (whereCondition.length > MAX_WHERE_CONDITION_LENGTH) {
      message.warning(`筛选条件最多 ${MAX_WHERE_CONDITION_LENGTH} 个字符`);
      return;
    }
    let parsedValueReplacements: ExportValueReplacement[] | undefined;
    try {
      parsedValueReplacements = parseValueReplacements();
    } catch (e: any) {
      message.warning(e?.message ?? "字段替换配置不正确");
      return;
    }
    setSubmitting(true);
    try {
      await api.createTask({
        group_id: groupID,
        conn_id: connID,
        kind: "export",
        scope,
        target_database: scope === "connection" ? undefined : database,
        target_schema: scope === "table" ? schema : undefined,
        target_table: scope === "table" ? table : undefined,
        export_format: scope === "table" ? exportFormat : "csv",
        where_condition: scope === "table" ? whereCondition.trim() : undefined,
        on_conflict_do_nothing:
          scope === "table" && exportFormat === "insert_sql" ? onConflictDoNothing : false,
        value_replacements: parsedValueReplacements,
      });
      message.success("任务已创建");
      onCreated();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "创建任务失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      title="新建导出任务"
      onCancel={onClose}
      onOk={submit}
      confirmLoading={submitting}
      okText="提交"
      cancelText="取消"
      destroyOnClose
      width={760}
    >
      <Form layout="vertical">
        <Form.Item label="连接组" required>
          <Select
            placeholder="选择连接组"
            value={groupID}
            onChange={(v) => {
              setGroupID(v);
              setConnID(undefined);
              setDatabase(undefined);
              setSchema(undefined);
              setTable(undefined);
              setColumns([]);
              setValueReplacements([]);
            }}
            options={eligibleGroups.map((g) => ({ label: g.name, value: g.id }))}
          />
        </Form.Item>

        <Form.Item label="连接" required>
          <Select
            placeholder="选择连接"
            value={connID}
            disabled={!groupID}
            onChange={(v) => {
              setConnID(v);
              setDatabase(undefined);
              setSchema(undefined);
              setTable(undefined);
              setColumns([]);
              setValueReplacements([]);
            }}
            options={connections.map((c) => ({
              label: `${c.name} (${c.driver})`,
              value: c.id,
            }))}
          />
        </Form.Item>

        <Form.Item label="导出范围" required>
          <Radio.Group
            value={scope}
            onChange={(e) => {
              const nextScope = e.target.value as TaskScope;
              setScope(nextScope);
              if (nextScope !== "table") {
                setExportFormat("csv");
                setWhereCondition("");
                setOnConflictDoNothing(false);
                setValueReplacements([]);
              }
            }}
          >
            <Radio.Button value="table">单表</Radio.Button>
            <Radio.Button value="database">整库</Radio.Button>
            <Radio.Button value="connection">整连接（所有库）</Radio.Button>
          </Radio.Group>
        </Form.Item>

        {scope !== "connection" && (
          <Form.Item label="数据库" required>
            <Select
              placeholder="选择数据库"
              value={database}
              disabled={!connID}
              onChange={(v) => {
                setDatabase(v);
                setSchema(undefined);
                setTable(undefined);
                setColumns([]);
                setValueReplacements([]);
              }}
              options={databases.map((d) => ({ label: d, value: d }))}
              showSearch
            />
          </Form.Item>
        )}

        {scope === "table" && (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: `${SCHEMA_SELECT_WIDTH}px minmax(${TABLE_SELECT_MIN_WIDTH}px, 1fr)`,
              gap: 12,
              alignItems: "start",
            }}
          >
            <Form.Item label="Schema" required style={{ width: SCHEMA_SELECT_WIDTH }}>
              <Select
                placeholder="选择 schema"
                value={schema}
                disabled={!database}
                onChange={(v) => {
                  setSchema(v);
                  setTable(undefined);
                  setColumns([]);
                  setValueReplacements([]);
                }}
                options={schemas.map((s) => ({ label: s, value: s }))}
                showSearch
              />
            </Form.Item>
            <Form.Item label="表" required style={{ flex: 1, minWidth: 0 }}>
              <Select
                placeholder="选择表"
                value={table}
                disabled={!schema}
                onChange={(v) => {
                  setTable(v);
                  setColumns([]);
                  setValueReplacements([]);
                }}
                options={tables.map((t) => ({ label: t, value: t }))}
                showSearch
                popupMatchSelectWidth={false}
                style={{ width: "100%" }}
                dropdownStyle={{ minWidth: tableSelectWidth, maxWidth: TABLE_SELECT_MAX_WIDTH }}
              />
            </Form.Item>
          </div>
        )}

        {scope === "table" && (
          <>
            <Form.Item label="筛选条件">
              <Input.TextArea
                value={whereCondition}
                onChange={(e) => setWhereCondition(e.target.value)}
                rows={3}
                maxLength={MAX_WHERE_CONDITION_LENGTH}
                placeholder="tenant_id = 1001 AND deleted_at IS NULL"
                showCount
              />
            </Form.Item>

            <Form.Item label="导出格式" required>
              <Radio.Group
                value={exportFormat}
                onChange={(e) => {
                  const nextFormat = e.target.value as ExportFormat;
                  setExportFormat(nextFormat);
                  if (nextFormat !== "insert_sql") {
                    setOnConflictDoNothing(false);
                    setValueReplacements([]);
                  }
                }}
              >
                <Radio.Button value="csv">CSV</Radio.Button>
                <Radio.Button value="insert_sql">INSERT SQL</Radio.Button>
              </Radio.Group>
            </Form.Item>

            {exportFormat === "insert_sql" && (
              <>
                <Form.Item>
                  <Checkbox
                    checked={onConflictDoNothing}
                    onChange={(e) => setOnConflictDoNothing(e.target.checked)}
                  >
                    {selectedConnection?.driver === "postgres"
                      ? "追加 ON CONFLICT DO NOTHING"
                      : "冲突时忽略"}
                  </Checkbox>
                </Form.Item>

                <Form.Item label="字段替换">
                  {valueReplacements.length > 0 && (
                    <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                      <Button
                        type="link"
                        size="small"
                        icon={<PlusOutlined />}
                        disabled={!columns.length}
                        title="添加字段替换"
                        onClick={() =>
                          setValueReplacements((items) => [
                            ...items,
                            createValueReplacementDraft(),
                          ])
                        }
                      >
                        添加
                      </Button>
                    </div>
                  )}
                  {valueReplacements.length === 0 ? (
                    <Button
                      icon={<PlusOutlined />}
                      disabled={!columns.length}
                      title="添加字段替换"
                      onClick={() => setValueReplacements([createValueReplacementDraft()])}
                    >
                      添加字段替换
                    </Button>
                  ) : (
                    <div style={{ display: "grid", gap: 12 }}>
                      {valueReplacements.map((item, index) => (
                        <div
                          key={item.id}
                          style={{
                            border: "1px solid #f0f0f0",
                            borderRadius: 6,
                            padding: 12,
                            background: "#fff",
                          }}
                        >
                          <div
                            style={{
                              display: "grid",
                              gridTemplateColumns: "minmax(180px, 1fr) 150px 32px",
                              gap: 8,
                              alignItems: "center",
                              marginBottom: 8,
                            }}
                          >
                            <Select
                              placeholder="选择列"
                              value={item.column}
                              options={columns.map((col) => ({
                                label: col.name,
                                value: col.name,
                              }))}
                              onChange={(column) => updateValueReplacement(item.id, { column })}
                              showSearch
                            />
                            <Select<ExportReplacementOnMissing>
                              value={item.onMissing}
                              options={[
                                { label: "未匹配保留", value: "keep" },
                                { label: "未匹配置空值", value: "empty" },
                              ]}
                              onChange={(onMissing) =>
                                updateValueReplacement(item.id, { onMissing })
                              }
                            />
                            <Button
                              aria-label={`删除第 ${index + 1} 个字段替换`}
                              title="删除字段替换"
                              icon={<DeleteOutlined />}
                              onClick={() =>
                                setValueReplacements((items) =>
                                  items.filter((x) => x.id !== item.id)
                                )
                              }
                            />
                          </div>
                          <Input.TextArea
                            value={item.mappingText}
                            onChange={(e) =>
                              updateValueReplacement(item.id, { mappingText: e.target.value })
                            }
                            rows={4}
                            placeholder={'{\n  "old_value": "new_value"\n}'}
                          />
                        </div>
                      ))}
                    </div>
                  )}
                </Form.Item>
              </>
            )}
          </>
        )}

        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          {scope === "table" && exportFormat === "insert_sql"
            ? "产物为 INSERT SQL，完成后可在任务列表点击下载。"
            : "产物为 CSV（多表自动打包 zip），完成后可在任务列表点击下载。"}
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}
