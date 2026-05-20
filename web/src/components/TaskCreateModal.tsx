import { useEffect, useMemo, useState } from "react";
import { App, Form, Modal, Radio, Select, Space, Typography } from "antd";
import { api } from "../api";
import type { Connection, Group, TaskScope } from "../types";
import { canDDL } from "../utils/role";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onCreated: () => void;
}

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
    }
  }, [open]);

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
      return;
    }
    api
      .listTables(connID, schema, database)
      .then((t) => setTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载表失败"));
  }, [connID, database, schema, scope, message]);

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
      width={560}
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
            }}
            options={connections.map((c) => ({
              label: `${c.name} (${c.driver})`,
              value: c.id,
            }))}
          />
        </Form.Item>

        <Form.Item label="导出范围" required>
          <Radio.Group value={scope} onChange={(e) => setScope(e.target.value)}>
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
              }}
              options={databases.map((d) => ({ label: d, value: d }))}
              showSearch
            />
          </Form.Item>
        )}

        {scope === "table" && (
          <Space style={{ display: "flex" }} size={12}>
            <Form.Item label="Schema" required style={{ flex: 1 }}>
              <Select
                placeholder="选择 schema"
                value={schema}
                disabled={!database}
                onChange={(v) => {
                  setSchema(v);
                  setTable(undefined);
                }}
                options={schemas.map((s) => ({ label: s, value: s }))}
                showSearch
              />
            </Form.Item>
            <Form.Item label="表" required style={{ flex: 1 }}>
              <Select
                placeholder="选择表"
                value={table}
                disabled={!schema}
                onChange={setTable}
                options={tables.map((t) => ({ label: t, value: t }))}
                showSearch
              />
            </Form.Item>
          </Space>
        )}

        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          产物为 CSV（多表自动打包 zip），完成后可在任务列表点击下载。
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}
