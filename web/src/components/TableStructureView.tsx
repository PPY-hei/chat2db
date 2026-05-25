import { useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Input, Modal, Select, Space, Switch, Table, Tag, Typography } from "antd";
import { PlusOutlined, DeleteOutlined, SaveOutlined, ReloadOutlined, CodeOutlined } from "@ant-design/icons";
import { api } from "../api";
import type { Role } from "../types";
import { canDDL } from "../utils/role";
import {
  buildAlterStatements,
  type ColumnDraft,
  type IndexDraft,
} from "../utils/ddl";

interface Props {
  connID: number;
  database?: string;
  schema: string;
  table: string;
  driver: string;
  role: Role;
}

let keySeq = 0;
const nextKey = () => `row-${++keySeq}`;

export default function TableStructureView({ connID, database, schema, table, driver, role }: Props) {
  const { message } = App.useApp();
  const editable = canDDL(role);

  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);

  // 原始快照（用于 diff）与可编辑草稿
  const origCols = useRef<ColumnDraft[]>([]);
  const origIdx = useRef<IndexDraft[]>([]);
  const [cols, setCols] = useState<ColumnDraft[]>([]);
  const [idxs, setIdxs] = useState<IndexDraft[]>([]);

  const load = async () => {
    if (!schema || !table) return;
    setLoading(true);
    try {
      const [c, i] = await Promise.all([
        api.listColumns(connID, schema, table, database),
        api.listIndexes(connID, schema, table, database),
      ]);
      const cd: ColumnDraft[] = c.map((x) => ({
        key: nextKey(),
        origName: x.name,
        name: x.name,
        data_type: x.data_type,
        nullable: x.nullable,
        default_value: x.default_value ?? null,
        comment: x.comment ?? null,
        is_primary: x.is_primary,
        auto_increment: !!x.auto_increment,
      }));
      const id: IndexDraft[] = i.map((x) => ({
        key: nextKey(),
        origName: x.name,
        name: x.name,
        columns: x.columns,
        unique: x.unique,
        primary: x.primary,
      }));
      origCols.current = cd.map((x) => ({ ...x }));
      origIdx.current = id.map((x) => ({ ...x, columns: [...x.columns] }));
      setCols(cd);
      setIdxs(id);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "结构加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connID, database, schema, table]);

  const statements = useMemo(
    () =>
      buildAlterStatements(driver, schema, table, origCols.current, cols, origIdx.current, idxs),
    [driver, schema, table, cols, idxs]
  );

  const patchCol = (key: string, patch: Partial<ColumnDraft>) =>
    setCols((prev) => prev.map((c) => (c.key === key ? { ...c, ...patch } : c)));
  const patchIdx = (key: string, patch: Partial<IndexDraft>) =>
    setIdxs((prev) => prev.map((i) => (i.key === key ? { ...i, ...patch } : i)));

  const addCol = () =>
    setCols((prev) => [
      ...prev,
      {
        key: nextKey(),
        name: "",
        data_type: driver === "mysql" ? "varchar(255)" : "varchar",
        nullable: true,
        default_value: null,
        comment: null,
        is_primary: false,
        auto_increment: false,
      },
    ]);

  const removeCol = (key: string) =>
    setCols((prev) => {
      const c = prev.find((x) => x.key === key);
      if (c && !c.origName) return prev.filter((x) => x.key !== key); // 新增列直接移除
      return prev.map((x) => (x.key === key ? { ...x, _deleted: !x._deleted } : x));
    });

  const addIdx = () =>
    setIdxs((prev) => [
      ...prev,
      { key: nextKey(), name: "", columns: [], unique: false, primary: false },
    ]);

  const removeIdx = (key: string) =>
    setIdxs((prev) => {
      const i = prev.find((x) => x.key === key);
      if (i && !i.origName) return prev.filter((x) => x.key !== key);
      return prev.map((x) => (x.key === key ? { ...x, _deleted: !x._deleted } : x));
    });

  const save = async () => {
    if (statements.length === 0) {
      message.info("没有需要执行的结构变更");
      return;
    }
    setSaving(true);
    try {
      const sql = statements.join(";\n") + ";";
      const res = await api.execute(connID, sql, database);
      if (res.error) {
        message.error("执行失败：" + res.error);
      } else {
        message.success("结构已更新");
        await load();
      }
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "执行失败");
    } finally {
      setSaving(false);
    }
  };

  const colNameOptions = cols
    .filter((c) => !c._deleted && c.name)
    .map((c) => ({ label: c.name, value: c.name }));

  const rowStyle = (deleted?: boolean) =>
    deleted ? { textDecoration: "line-through", opacity: 0.5 } : undefined;

  const columnCols = [
    {
      title: "列名",
      dataIndex: "name",
      width: 180,
      render: (_: any, r: ColumnDraft) =>
        editable ? (
          <Input
            size="small"
            value={r.name}
            disabled={r._deleted}
            onChange={(e) => patchCol(r.key, { name: e.target.value })}
          />
        ) : (
          <span>{r.name}</span>
        ),
    },
    {
      title: "类型",
      dataIndex: "data_type",
      width: 160,
      render: (_: any, r: ColumnDraft) =>
        editable ? (
          <Input
            size="small"
            value={r.data_type}
            disabled={r._deleted}
            onChange={(e) => patchCol(r.key, { data_type: e.target.value })}
          />
        ) : (
          <span>{r.data_type}</span>
        ),
    },
    {
      title: "可空",
      dataIndex: "nullable",
      width: 64,
      align: "center" as const,
      render: (_: any, r: ColumnDraft) =>
        editable ? (
          <Switch
            size="small"
            checked={r.nullable}
            disabled={r._deleted}
            onChange={(v) => patchCol(r.key, { nullable: v })}
          />
        ) : r.nullable ? (
          "✓"
        ) : (
          ""
        ),
    },
    {
      title: "默认值",
      dataIndex: "default_value",
      width: 150,
      render: (_: any, r: ColumnDraft) =>
        editable ? (
          <Input
            size="small"
            placeholder="SQL 表达式"
            value={r.default_value ?? ""}
            disabled={r._deleted}
            onChange={(e) => patchCol(r.key, { default_value: e.target.value || null })}
          />
        ) : (
          <span>{r.default_value}</span>
        ),
    },
    {
      title: "约束",
      width: 110,
      render: (_: any, r: ColumnDraft) => (
        <Space size={4}>
          {r.is_primary && <Tag color="orange">PK</Tag>}
          {r.auto_increment && <Tag color="geekblue">AI</Tag>}
        </Space>
      ),
    },
    {
      title: "注释",
      dataIndex: "comment",
      render: (_: any, r: ColumnDraft) =>
        editable ? (
          <Input
            size="small"
            value={r.comment ?? ""}
            disabled={r._deleted}
            onChange={(e) => patchCol(r.key, { comment: e.target.value || null })}
          />
        ) : (
          <span>{r.comment}</span>
        ),
    },
  ];

  if (editable) {
    columnCols.push({
      title: "",
      width: 50,
      align: "center" as const,
      render: (_: any, r: ColumnDraft) => (
        <Button
          size="small"
          type="text"
          danger
          icon={<DeleteOutlined />}
          onClick={() => removeCol(r.key)}
        />
      ),
    } as any);
  }

  const indexCols = [
    {
      title: "索引名",
      dataIndex: "name",
      width: 220,
      render: (_: any, r: IndexDraft) =>
        editable && !r.origName ? (
          <Input
            size="small"
            value={r.name}
            onChange={(e) => patchIdx(r.key, { name: e.target.value })}
          />
        ) : (
          <Space size={4}>
            <span>{r.name}</span>
            {r.primary && <Tag color="orange">PK</Tag>}
          </Space>
        ),
    },
    {
      title: "唯一",
      dataIndex: "unique",
      width: 64,
      align: "center" as const,
      render: (_: any, r: IndexDraft) =>
        editable && !r.origName ? (
          <Switch size="small" checked={r.unique} onChange={(v) => patchIdx(r.key, { unique: v })} />
        ) : r.unique ? (
          "✓"
        ) : (
          ""
        ),
    },
    {
      title: "列",
      dataIndex: "columns",
      render: (_: any, r: IndexDraft) =>
        editable && !r.origName ? (
          <Select
            size="small"
            mode="multiple"
            style={{ width: "100%" }}
            value={r.columns}
            options={colNameOptions}
            onChange={(v) => patchIdx(r.key, { columns: v })}
          />
        ) : (
          <span>{r.columns.join(", ")}</span>
        ),
    },
  ];

  if (editable) {
    indexCols.push({
      title: "",
      width: 50,
      align: "center" as const,
      render: (_: any, r: IndexDraft) =>
        r.primary ? null : (
          <Button
            size="small"
            type="text"
            danger
            icon={<DeleteOutlined />}
            onClick={() => removeIdx(r.key)}
          />
        ),
    } as any);
  }

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div className="sql-toolbar">
        <Space>
          <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={loading}>
            刷新
          </Button>
          <Button size="small" icon={<CodeOutlined />} onClick={() => setPreviewOpen(true)}>
            SQL Preview
            {statements.length > 0 && ` (${statements.length})`}
          </Button>
          {editable && (
            <Button
              size="small"
              type="primary"
              icon={<SaveOutlined />}
              loading={saving}
              disabled={statements.length === 0}
              onClick={save}
            >
              保存
            </Button>
          )}
          {!editable && <Tag color="default">只读（需 admin 及以上可改结构）</Tag>}
        </Space>
      </div>
      <div style={{ flex: 1, overflow: "auto", padding: 12 }}>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          列
        </Typography.Title>
        <Table
          size="small"
          bordered
          rowKey="key"
          loading={loading}
          pagination={false}
          dataSource={cols}
          columns={columnCols as any}
          onRow={(r) => ({ style: rowStyle(r._deleted) })}
        />
        {editable && (
          <Button size="small" icon={<PlusOutlined />} onClick={addCol} style={{ marginTop: 8 }}>
            添加列
          </Button>
        )}

        <Typography.Title level={5} style={{ marginTop: 24 }}>
          索引
        </Typography.Title>
        <Table
          size="small"
          bordered
          rowKey="key"
          pagination={false}
          dataSource={idxs}
          columns={indexCols as any}
          onRow={(r) => ({ style: rowStyle(r._deleted) })}
        />
        {editable && (
          <Button size="small" icon={<PlusOutlined />} onClick={addIdx} style={{ marginTop: 8 }}>
            添加索引
          </Button>
        )}
      </div>

      <Modal
        open={previewOpen}
        title="将要执行的 SQL"
        onCancel={() => setPreviewOpen(false)}
        footer={[
          <Button key="close" onClick={() => setPreviewOpen(false)}>
            关闭
          </Button>,
        ]}
        width={720}
      >
        {statements.length === 0 ? (
          <Typography.Text type="secondary">暂无结构变更</Typography.Text>
        ) : (
          <pre
            style={{
              background: "#1e1e1e",
              color: "#d4d4d4",
              padding: 12,
              borderRadius: 4,
              maxHeight: 400,
              overflow: "auto",
            }}
          >
            {statements.join(";\n") + ";"}
          </pre>
        )}
      </Modal>
    </div>
  );
}
