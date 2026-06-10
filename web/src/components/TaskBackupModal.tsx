import { useEffect, useMemo, useState } from "react";
import { App, Form, Input, Modal, Select, Space, Typography } from "antd";
import dayjs from "dayjs";
import { api } from "../api";
import type { Connection, Group } from "../types";
import { canDDL } from "../utils/role";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onCreated: () => void;
}

const buildDefaultBackupTable = (table: string) =>
  table ? `${table}_backup_${dayjs().format("YYYYMMDD_HHmmss")}` : "";

export default function TaskBackupModal({ open, groups, onClose, onCreated }: Props) {
  const { message } = App.useApp();
  const [submitting, setSubmitting] = useState(false);

  const [groupID, setGroupID] = useState<number | undefined>();
  const [connections, setConnections] = useState<Connection[]>([]);
  const [connID, setConnID] = useState<number | undefined>();
  const [databases, setDatabases] = useState<string[]>([]);
  const [database, setDatabase] = useState<string | undefined>();
  const [schemas, setSchemas] = useState<string[]>([]);
  const [schema, setSchema] = useState<string | undefined>();
  const [tables, setTables] = useState<string[]>([]);
  const [table, setTable] = useState<string | undefined>();
  const [backupTable, setBackupTable] = useState("");

  const eligibleGroups = useMemo(
    () => groups.filter((g) => g.role === "editor" || canDDL(g.role)),
    [groups]
  );

  useEffect(() => {
    if (!open) {
      setSubmitting(false);
      setGroupID(undefined);
      setConnections([]);
      setConnID(undefined);
      setDatabases([]);
      setDatabase(undefined);
      setSchemas([]);
      setSchema(undefined);
      setTables([]);
      setTable(undefined);
      setBackupTable("");
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
    if (!connID || !database) {
      setSchemas([]);
      setSchema(undefined);
      return;
    }
    api
      .listSchemas(connID, database)
      .then((s) => {
        const names = s.map((x) => x.name);
        setSchemas(names);
        if (names.length === 1) setSchema(names[0]);
      })
      .catch((e) => message.error(e?.response?.data?.error ?? "加载 schema 失败"));
  }, [connID, database, message]);

  useEffect(() => {
    if (!connID || !database || !schema) {
      setTables([]);
      setTable(undefined);
      return;
    }
    api
      .listTables(connID, schema, database)
      .then((t) => setTables(t.filter((x) => x.kind === "table").map((x) => x.name)))
      .catch((e) => message.error(e?.response?.data?.error ?? "加载表失败"));
  }, [connID, database, schema, message]);

  const submit = async () => {
    if (!groupID || !connID || !database || !schema || !table) {
      message.warning("请选择连接组、连接、数据库、schema 和源表");
      return;
    }
    const dest = backupTable.trim();
    if (!dest) {
      message.warning("请填写备份表名");
      return;
    }
    if (dest === table) {
      message.warning("备份表名不能与源表相同");
      return;
    }
    setSubmitting(true);
    try {
      await api.createTask({
        group_id: groupID,
        conn_id: connID,
        kind: "backup",
        scope: "table",
        target_database: database,
        target_schema: schema,
        target_table: table,
        dest_database: database,
        dest_schema: schema,
        dest_table: dest,
        backup_table: dest,
      });
      message.success("备份任务已创建");
      onCreated();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "创建备份任务失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      title="新建备份任务"
      onCancel={onClose}
      onOk={submit}
      confirmLoading={submitting}
      okText="确定执行"
      cancelText="取消"
      destroyOnClose
      width={720}
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
              setBackupTable("");
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
              setBackupTable("");
            }}
            options={connections.map((c) => ({
              label: `${c.name} (${c.driver})`,
              value: c.id,
            }))}
          />
        </Form.Item>

        <Form.Item label="数据库" required>
          <Select
            placeholder="选择数据库"
            value={database}
            disabled={!connID}
            onChange={(v) => {
              setDatabase(v);
              setSchema(undefined);
              setTable(undefined);
              setBackupTable("");
            }}
            options={databases.map((d) => ({ label: d, value: d }))}
            showSearch
          />
        </Form.Item>

        <Space style={{ display: "flex" }} size={12}>
          <Form.Item label="Schema" required style={{ flex: 1 }}>
            <Select
              placeholder="选择 schema"
              value={schema}
              disabled={!database}
              onChange={(v) => {
                setSchema(v);
                setTable(undefined);
                setBackupTable("");
              }}
              options={schemas.map((s) => ({ label: s, value: s }))}
              showSearch
            />
          </Form.Item>
          <Form.Item label="源表" required style={{ flex: 1 }}>
            <Select
              placeholder="选择源表"
              value={table}
              disabled={!schema}
              onChange={(v) => {
                setTable(v);
                setBackupTable(buildDefaultBackupTable(v));
              }}
              options={tables.map((t) => ({ label: t, value: t }))}
              showSearch
              popupMatchSelectWidth={false}
            />
          </Form.Item>
        </Space>

        <Form.Item label="备份表名" required>
          <Input
            value={backupTable}
            onChange={(e) => setBackupTable(e.target.value)}
            placeholder="源表_backup_YYYYMMDD_HHmmss"
          />
        </Form.Item>

        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          备份任务会在同一个数据库和 schema 下创建新表并复制源表数据；目标表已存在时任务会失败，不会覆盖已有表。
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}
