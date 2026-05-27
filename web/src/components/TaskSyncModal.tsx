import { useEffect, useMemo, useState } from "react";
import { App, Form, Modal, Radio, Select, Space, Typography } from "antd";
import { api } from "../api";
import type { Connection, Group, TaskKind, TaskScope } from "../types";
import { canDDL } from "../utils/role";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onCreated: () => void;
}

/**
 * TaskSyncModal —— 创建同步任务（表结构同步 / 数据同步）。
 *
 * 联动顺序：
 *   1. 选择任务类型（schema_sync / data_sync）
 *   2. 选择组
 *   3. 选择源连接 → 源数据库 → 源 schema → 源表
 *   4. 选择目标连接 → 目标数据库 → 目标 schema → 目标表
 *
 * 设计取舍：
 *   - 仅 admin/owner/editor 可创建；
 *   - 当前版本仅支持单表同步（scope=table）；
 *   - 数据库和表的下拉数据按需 lazy 调用现有元数据接口。
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
      return;
    }
    api
      .listTables(srcConnID, srcSchema, srcDatabase)
      .then((t) => setSrcTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载源表失败"));
  }, [srcConnID, srcDatabase, srcSchema, message]);

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

  const submit = async () => {
    if (!groupID || !srcConnID || !destConnID) {
      message.warning("请选择连接组、源连接和目标连接");
      return;
    }
    if (!srcDatabase || !srcSchema || !srcTable) {
      message.warning("请选择源数据库、schema 和表");
      return;
    }
    if (!destDatabase || !destSchema || !destTable) {
      message.warning("请选择目标数据库、schema 和表");
      return;
    }

    setSubmitting(true);
    try {
      await api.createTask({
        group_id: groupID,
        conn_id: srcConnID,
        target_conn_id: destConnID,
        kind,
        scope: "table",
        target_database: srcDatabase,
        target_schema: srcSchema,
        target_table: srcTable,
        dest_database: destDatabase,
        dest_schema: destSchema,
        dest_table: destTable,
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
      width={720}
    >
      <Form layout="vertical">
        <Form.Item label="任务类型" required>
          <Radio.Group value={kind} onChange={(e) => setKind(e.target.value)}>
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
              }}
              options={srcSchemas.map((s) => ({ label: s, value: s }))}
              showSearch
            />
          </Form.Item>
          <Form.Item label="源表" required style={{ flex: 1 }}>
            <Select
              placeholder="选择表"
              value={srcTable}
              disabled={!srcSchema}
              onChange={setSrcTable}
              options={srcTables.map((t) => ({ label: t, value: t }))}
              showSearch
              popupMatchSelectWidth={false}
              style={{ minWidth: 150, maxWidth: 300 }}
            />
          </Form.Item>
        </Space>

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
          <Form.Item label="目标表" required style={{ flex: 1 }}>
            <Select
              placeholder="选择表"
              value={destTable}
              disabled={!destSchema}
              onChange={setDestTable}
              options={destTables.map((t) => ({ label: t, value: t }))}
              showSearch
              popupMatchSelectWidth={false}
              style={{ minWidth: 150, maxWidth: 300 }}
            />
          </Form.Item>
        </Space>

        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          {kind === "schema_sync"
            ? "表结构同步：对比源表和目标表的结构差异，自动生成并执行 DDL 语句同步字段、索引等。"
            : "表数据同步：从源表读取所有数据，批量插入到目标表（仅同步匹配的列）。"}
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}
