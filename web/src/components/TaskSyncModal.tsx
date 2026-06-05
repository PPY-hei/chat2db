import { useEffect, useMemo, useState } from "react";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { App, Button, Form, Input, Modal, Radio, Select, Space, Typography } from "antd";
import { api } from "../api";
import type {
  ColumnInfo,
  Connection,
  ExportReplacementOnMissing,
  ExportValueReplacement,
  Group,
  TaskKind,
  TaskScope,
} from "../types";
import { canDDL } from "../utils/role";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onCreated: () => void;
}

const MAX_WHERE_CONDITION_LENGTH = 200000;

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
 * TaskSyncModal —— 创建同步任务（表结构同步 / 数据同步）。
 *
 * 联动顺序：
 *   1. 选择任务类型（schema_sync / data_sync）
 *   2. 选择组
 *   3. 选择源连接 → 源数据库 → 源 schema → （可选）源表
 *   4. 选择目标连接 → 目标数据库 → 目标 schema → （可选）目标表
 *
 * 设计取舍：
 *   - 仅 admin/owner/editor 可创建；
 *   - 表名为可选：
 *       · 选了源表（目标表可选）→ scope=table，单表同步；
 *       · 不选表（仅到 schema）→ scope=schema，遍历源 schema 下所有表，按同名映射到目标 schema；
 *   - 目标表不存在时（无论 scope=table 或 schema）会按源表结构自动建表后再同步数据。
 */
export default function TaskSyncModal({ open, groups, onClose, onCreated }: Props) {
  const { message } = App.useApp();
  const [submitting, setSubmitting] = useState(false);

  const [kind, setKind] = useState<TaskKind>("schema_sync");
  const [groupID, setGroupID] = useState<number | undefined>();

  // 源连接相关
  const [srcConnections, setSrcConnections] = useState<Connection[]>([]);
  const [srcConnID, setSrcConnID] = useState<number | undefined>();
  const [srcDatabases, setSrcDatabases] = useState<string[]>([]);
  const [srcDatabase, setSrcDatabase] = useState<string | undefined>();
  const [srcSchemas, setSrcSchemas] = useState<string[]>([]);
  const [srcSchema, setSrcSchema] = useState<string | undefined>();
  const [srcTables, setSrcTables] = useState<string[]>([]);
  const [srcTable, setSrcTable] = useState<string | undefined>();
  const [srcColumns, setSrcColumns] = useState<ColumnInfo[]>([]);
  const [whereCondition, setWhereCondition] = useState("");
  const [valueReplacements, setValueReplacements] = useState<ValueReplacementDraft[]>([]);

  // 目标连接相关
  const [destConnections, setDestConnections] = useState<Connection[]>([]);
  const [destConnID, setDestConnID] = useState<number | undefined>();
  const [destDatabases, setDestDatabases] = useState<string[]>([]);
  const [destDatabase, setDestDatabase] = useState<string | undefined>();
  const [destSchemas, setDestSchemas] = useState<string[]>([]);
  const [destSchema, setDestSchema] = useState<string | undefined>();
  const [destTables, setDestTables] = useState<string[]>([]);
  const [destTable, setDestTable] = useState<string | undefined>();

  // 仅展示当前用户至少为 editor 的组
  const eligibleGroups = useMemo(
    () => groups.filter((g) => g.role === "editor" || canDDL(g.role)),
    [groups]
  );

  useEffect(() => {
    if (!open) {
      // 关闭时重置
      setKind("schema_sync");
      setGroupID(undefined);
      setSrcConnections([]);
      setSrcConnID(undefined);
      setSrcDatabases([]);
      setSrcDatabase(undefined);
      setSrcSchemas([]);
      setSrcSchema(undefined);
      setSrcTables([]);
      setSrcTable(undefined);
      setSrcColumns([]);
      setWhereCondition("");
      setValueReplacements([]);
      setDestConnections([]);
      setDestConnID(undefined);
      setDestDatabases([]);
      setDestDatabase(undefined);
      setDestSchemas([]);
      setDestSchema(undefined);
      setDestTables([]);
      setDestTable(undefined);
    }
  }, [open]);

  const isSingleTableDataSync = kind === "data_sync" && !!srcTable;

  // 加载源连接列表
  useEffect(() => {
    if (!groupID) {
      setSrcConnections([]);
      setSrcConnID(undefined);
      return;
    }
    api
      .listConnections(groupID)
      .then(setSrcConnections)
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源连接失败"));
  }, [groupID, message]);

  // 加载目标连接列表（与源连接相同）
  useEffect(() => {
    if (!groupID) {
      setDestConnections([]);
      setDestConnID(undefined);
      return;
    }
    api
      .listConnections(groupID)
      .then(setDestConnections)
      .catch((e) => message.error(e?.response?.data?.error ?? "加载目标连接失败"));
  }, [groupID, message]);

  // 加载源数据库列表
  useEffect(() => {
    if (!srcConnID) {
      setSrcDatabases([]);
      setSrcDatabase(undefined);
      return;
    }
    api
      .listDatabases(srcConnID)
      .then((dbs) => setSrcDatabases(dbs.map((d) => d.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源数据库失败"));
  }, [srcConnID, message]);

  // 加载源 schema 列表
  useEffect(() => {
    if (!srcConnID || !srcDatabase) {
      setSrcSchemas([]);
      setSrcSchema(undefined);
      return;
    }
    api
      .listSchemas(srcConnID, srcDatabase)
      .then((s) => {
        const names = s.map((x) => x.name);
        setSrcSchemas(names);
        if (names.length === 1) setSrcSchema(names[0]);
      })
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源 schema 失败"));
  }, [srcConnID, srcDatabase, message]);

  // 加载源表列表
  useEffect(() => {
    if (!srcConnID || !srcDatabase || !srcSchema) {
      setSrcTables([]);
      setSrcTable(undefined);
      setSrcColumns([]);
      setWhereCondition("");
      setValueReplacements([]);
      return;
    }
    api
      .listTables(srcConnID, srcSchema, srcDatabase)
      .then((t) => setSrcTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源表失败"));
  }, [srcConnID, srcDatabase, srcSchema, message]);

  useEffect(() => {
    if (!srcConnID || !srcDatabase || !srcSchema || !srcTable) {
      setSrcColumns([]);
      return;
    }
    api
      .listColumns(srcConnID, srcSchema, srcTable, srcDatabase)
      .then(setSrcColumns)
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源列失败"));
  }, [srcConnID, srcDatabase, srcSchema, srcTable, message]);

  // 加载目标数据库列表
  useEffect(() => {
    if (!destConnID) {
      setDestDatabases([]);
      setDestDatabase(undefined);
      return;
    }
    api
      .listDatabases(destConnID)
      .then((dbs) => setDestDatabases(dbs.map((d) => d.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载目标数据库失败"));
  }, [destConnID, message]);

  // 加载目标 schema 列表
  useEffect(() => {
    if (!destConnID || !destDatabase) {
      setDestSchemas([]);
      setDestSchema(undefined);
      return;
    }
    api
      .listSchemas(destConnID, destDatabase)
      .then((s) => {
        const names = s.map((x) => x.name);
        setDestSchemas(names);
        if (names.length === 1) setDestSchema(names[0]);
      })
      .catch((e) => message.error(e?.response?.data?.error ?? "加载目标 schema 失败"));
  }, [destConnID, destDatabase, message]);

  // 加载目标表列表
  useEffect(() => {
    if (!destConnID || !destDatabase || !destSchema) {
      setDestTables([]);
      setDestTable(undefined);
      return;
    }
    api
      .listTables(destConnID, destSchema, destDatabase)
      .then((t) => setDestTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载目标表失败"));
  }, [destConnID, destDatabase, destSchema, message]);

  const updateValueReplacement = (id: number, patch: Partial<ValueReplacementDraft>) => {
    setValueReplacements((items) =>
      items.map((item) => (item.id === id ? { ...item, ...patch } : item))
    );
  };

  const parseValueReplacements = (): ExportValueReplacement[] | undefined => {
    if (!isSingleTableDataSync || valueReplacements.length === 0) {
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
    if (!groupID || !srcConnID || !destConnID) {
      message.warning("请选择连接组、源连接和目标连接");
      return;
    }
    if (!srcDatabase || !srcSchema) {
      message.warning("请选择源数据库和 schema");
      return;
    }
    if (!destDatabase || !destSchema) {
      message.warning("请选择目标数据库和 schema");
      return;
    }
    if (kind !== "data_sync" && (whereCondition.trim() || valueReplacements.length > 0)) {
      message.warning("筛选条件和字段替换仅支持表数据同步");
      return;
    }
    if (!srcTable && (whereCondition.trim() || valueReplacements.length > 0)) {
      message.warning("筛选条件和字段替换仅支持单表同步");
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

    // 表是可选的；单表时目标表可留空，后端默认使用源表名
    const hasSrcTable = !!srcTable;
    const hasDestTable = !!destTable;
    if (!hasSrcTable && hasDestTable) {
      message.warning("未选择源表时不能单独选择目标表");
      return;
    }
    const scope: TaskScope = hasSrcTable ? "table" : "schema";

    setSubmitting(true);
    try {
      await api.createTask({
        group_id: groupID,
        conn_id: srcConnID,
        target_conn_id: destConnID,
        kind,
        scope,
        target_database: srcDatabase,
        target_schema: srcSchema,
        target_table: srcTable,
        dest_database: destDatabase,
        dest_schema: destSchema,
        dest_table: destTable,
        where_condition: isSingleTableDataSync ? whereCondition.trim() : undefined,
        value_replacements: parsedValueReplacements,
      });
      message.success("同步任务已创建");
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
      title="新建同步任务"
      onCancel={onClose}
      onOk={submit}
      confirmLoading={submitting}
      okText="提交"
      cancelText="取消"
      destroyOnClose
      width={760}
    >
      <Form layout="vertical">
        <Form.Item label="任务类型" required>
          <Radio.Group
            value={kind}
            onChange={(e) => {
              const nextKind = e.target.value as TaskKind;
              setKind(nextKind);
              if (nextKind !== "data_sync") {
                setWhereCondition("");
                setValueReplacements([]);
              }
            }}
          >
            <Radio.Button value="schema_sync">表结构同步</Radio.Button>
            <Radio.Button value="data_sync">表数据同步</Radio.Button>
          </Radio.Group>
        </Form.Item>

        <Form.Item label="连接组" required>
          <Select
            placeholder="选择连接组"
            value={groupID}
            onChange={(v) => {
              setGroupID(v);
              setSrcConnID(undefined);
              setDestConnID(undefined);
            }}
            options={eligibleGroups.map((g) => ({ label: g.name, value: g.id }))}
          />
        </Form.Item>

        <Typography.Title level={5} style={{ marginTop: 16 }}>
          源连接
        </Typography.Title>

        <Form.Item label="源连接" required>
          <Select
            placeholder="选择源连接"
            value={srcConnID}
            disabled={!groupID}
            onChange={(v) => {
              setSrcConnID(v);
              setSrcDatabase(undefined);
              setSrcSchema(undefined);
              setSrcTable(undefined);
              setSrcColumns([]);
              setWhereCondition("");
              setValueReplacements([]);
            }}
            options={srcConnections.map((c) => ({
              label: `${c.name} (${c.driver})`,
              value: c.id,
            }))}
          />
        </Form.Item>

        <Form.Item label="源数据库" required>
          <Select
            placeholder="选择源数据库"
            value={srcDatabase}
            disabled={!srcConnID}
            onChange={(v) => {
              setSrcDatabase(v);
              setSrcSchema(undefined);
              setSrcTable(undefined);
              setSrcColumns([]);
              setWhereCondition("");
              setValueReplacements([]);
            }}
            options={srcDatabases.map((d) => ({ label: d, value: d }))}
            showSearch
          />
        </Form.Item>

        <Space style={{ display: "flex" }} size={12}>
          <Form.Item label="源 Schema" required style={{ flex: 1 }}>
            <Select
              placeholder="选择 schema"
              value={srcSchema}
              disabled={!srcDatabase}
              onChange={(v) => {
                setSrcSchema(v);
                setSrcTable(undefined);
                setSrcColumns([]);
                setWhereCondition("");
                setValueReplacements([]);
              }}
              options={srcSchemas.map((s) => ({ label: s, value: s }))}
              showSearch
            />
          </Form.Item>
          <Form.Item label="源表（可选）" style={{ flex: 1 }}>
            <Select
              placeholder="不选则同步整个 schema"
              value={srcTable}
              disabled={!srcSchema}
              onChange={(v) => {
                setSrcTable(v);
                setSrcColumns([]);
                setWhereCondition("");
                setValueReplacements([]);
                if (!v) setDestTable(undefined);
              }}
              options={srcTables.map((t) => ({ label: t, value: t }))}
              showSearch
              allowClear
              popupMatchSelectWidth={false}
              style={{ minWidth: 150, maxWidth: 300 }}
            />
          </Form.Item>
        </Space>

        {isSingleTableDataSync && (
          <>
            <Form.Item label="源数据筛选条件">
              <Input.TextArea
                value={whereCondition}
                onChange={(e) => setWhereCondition(e.target.value)}
                rows={3}
                maxLength={MAX_WHERE_CONDITION_LENGTH}
                placeholder="tenant_id = 1001 AND deleted_at IS NULL"
                showCount
              />
            </Form.Item>

            <Form.Item label="字段替换">
              {valueReplacements.length > 0 && (
                <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                  <Button
                    type="link"
                    size="small"
                    icon={<PlusOutlined />}
                    disabled={!srcColumns.length}
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
                  disabled={!srcColumns.length}
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
                          options={srcColumns.map((col) => ({
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

        <Typography.Title level={5} style={{ marginTop: 16 }}>
          目标连接
        </Typography.Title>

        <Form.Item label="目标连接" required>
          <Select
            placeholder="选择目标连接"
            value={destConnID}
            disabled={!groupID}
            onChange={(v) => {
              setDestConnID(v);
              setDestDatabase(undefined);
              setDestSchema(undefined);
              setDestTable(undefined);
            }}
            options={destConnections.map((c) => ({
              label: `${c.name} (${c.driver})`,
              value: c.id,
            }))}
          />
        </Form.Item>

        <Form.Item label="目标数据库" required>
          <Select
            placeholder="选择目标数据库"
            value={destDatabase}
            disabled={!destConnID}
            onChange={(v) => {
              setDestDatabase(v);
              setDestSchema(undefined);
              setDestTable(undefined);
            }}
            options={destDatabases.map((d) => ({ label: d, value: d }))}
            showSearch
          />
        </Form.Item>

        <Space style={{ display: "flex" }} size={12}>
          <Form.Item label="目标 Schema" required style={{ flex: 1 }}>
            <Select
              placeholder="选择 schema"
              value={destSchema}
              disabled={!destDatabase}
              onChange={(v) => {
                setDestSchema(v);
                setDestTable(undefined);
              }}
              options={destSchemas.map((s) => ({ label: s, value: s }))}
              showSearch
            />
          </Form.Item>
          <Form.Item label="目标表（可选）" style={{ flex: 1 }}>
            <Select
              placeholder={srcTable ? "不选则使用源表名" : "不选则按同名映射到目标 schema"}
              value={destTable}
              disabled={!destSchema}
              onChange={setDestTable}
              options={destTables.map((t) => ({ label: t, value: t }))}
              showSearch
              allowClear
              popupMatchSelectWidth={false}
              style={{ minWidth: 150, maxWidth: 300 }}
            />
          </Form.Item>
        </Space>

        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          {kind === "schema_sync"
            ? "表结构同步：对比源表与目标表结构差异，生成并执行 DDL 同步字段、索引等。不选表则遍历源 schema 下所有表，按同名映射到目标 schema；目标表不存在会按源表结构自动建表。"
            : "表数据同步：从源表读取数据，批量插入到目标表（仅同步同名列）。单表同步可填写源数据筛选条件和字段替换；目标表不选时使用源表名。不选源表则遍历源 schema 下所有表，按同名映射到目标 schema。"}
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}
