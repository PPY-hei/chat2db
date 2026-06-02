import { useCallback, useEffect, useMemo, useState } from "react";
import {
  App,
  Button,
  DatePicker,
  Drawer,
  Input,
  Popconfirm,
  Progress,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  DeleteOutlined,
  DownloadOutlined,
  PlusOutlined,
  ReloadOutlined,
  StopOutlined,
} from "@ant-design/icons";
import dayjs, { Dayjs } from "dayjs";
import type { ColumnsType } from "antd/es/table";
import { api, getToken } from "../api";
import type {
  Connection,
  Group,
  Task,
  TaskKind,
  TaskPage,
  TaskScope,
  TaskStatus,
} from "../types";
import TaskCreateModal from "./TaskCreateModal";
import TaskSyncModal from "./TaskSyncModal";

void ({} as Connection); // 占位：保留 import 以便后续扩展（创建表单内部用到）


interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
}

const STATUS_COLOR: Record<TaskStatus, string> = {
  pending: "default",
  running: "processing",
  succeeded: "green",
  failed: "red",
  canceled: "warning",
};

const STATUS_LABEL: Record<TaskStatus, string> = {
  pending: "排队中",
  running: "执行中",
  succeeded: "成功",
  failed: "失败",
  canceled: "已取消",
};

const KIND_LABEL: Record<TaskKind, string> = {
  export: "导出",
  import: "导入",
  schema_sync: "表结构同步",
  data_sync: "表数据同步",
};

const SCOPE_LABEL: Record<TaskScope, string> = {
  connection: "整连接",
  database: "整库",
  schema: "整 schema",
  table: "单表",
};

const DEFAULT_PAGE_SIZE = 20;
const POLL_INTERVAL_MS = 5000;

export default function TaskListModal({ open, groups, onClose }: Props) {
  const { message, modal } = App.useApp();

  const [range, setRange] = useState<[Dayjs, Dayjs]>(() => [
    dayjs().subtract(30, "day"),
    dayjs(),
  ]);
  const [keyword, setKeyword] = useState("");
  const [committedKeyword, setCommittedKeyword] = useState("");
  const [groupID, setGroupID] = useState<number | undefined>(undefined);
  const [kind, setKind] = useState<TaskKind | undefined>(undefined);
  const [scope, setScope] = useState<TaskScope | undefined>(undefined);
  const [status, setStatus] = useState<TaskStatus | undefined>(undefined);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [data, setData] = useState<TaskPage | null>(null);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [syncOpen, setSyncOpen] = useState(false);

  const visibleGroupIDs = data?.visible_group_ids ?? [];
  const groupOptions = useMemo(
    () =>
      groups
        .filter((g) => visibleGroupIDs.includes(g.id))
        .map((g) => ({ label: `${g.name} (#${g.id})`, value: g.id })),
    [groups, visibleGroupIDs]
  );

  const reload = useCallback(async () => {
    if (!open) return;
    setLoading(true);
    try {
      const resp = await api.listTasks({
        from: range[0].toISOString(),
        to: range[1].toISOString(),
        keyword: committedKeyword || undefined,
        group_id: groupID,
        kind,
        scope,
        status,
        page,
        size: pageSize,
      });
      setData(resp);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载任务列表失败");
    } finally {
      setLoading(false);
    }
  }, [open, range, committedKeyword, groupID, kind, scope, status, page, pageSize, message]);

  useEffect(() => {
    reload();
  }, [reload]);

  // 当列表里存在 pending/running 任务时启用轮询。
  // 避免一直 setInterval 给后端压力，没有"活动任务"时退避。
  useEffect(() => {
    if (!open) return;
    const hasActive = (data?.items ?? []).some(
      (t) => t.status === "pending" || t.status === "running"
    );
    if (!hasActive) return;
    const timer = setInterval(reload, POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [open, data, reload]);

  const handleCancel = async (id: number) => {
    try {
      await api.cancelTask(id);
      message.success("已请求取消");
      reload();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "取消失败");
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await api.deleteTask(id);
      message.success("已删除");
      reload();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "删除失败");
    }
  };

  const handleDownload = (id: number) => {
    // 带上 JWT 用 fetch 抓 blob，再触发浏览器下载。
    const token = getToken();
    fetch(api.downloadTaskUrl(id), {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then(async (r) => {
        if (!r.ok) {
          const t = await r.text().catch(() => "");
          throw new Error(t || `HTTP ${r.status}`);
        }
        const cd = r.headers.get("content-disposition") || "";
        const m = cd.match(/filename="?([^";]+)"?/i);
        const filename = m?.[1] || `task-${id}`;
        const blob = await r.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
      })
      .catch((e) => {
        modal.error({ title: "下载失败", content: String(e.message ?? e) });
      });
  };

  const columns: ColumnsType<Task> = useMemo(
    () => [
      { title: "ID", dataIndex: "id", width: 70 },
      {
        title: "类型",
        dataIndex: "kind",
        width: 80,
        render: (k: TaskKind) => <Tag>{KIND_LABEL[k] ?? k}</Tag>,
      },
      {
        title: "范围",
        dataIndex: "scope",
        width: 90,
        render: (s: TaskScope) => <Tag color="blue">{SCOPE_LABEL[s] ?? s}</Tag>,
      },
      {
        title: "目标",
        width: 280,
        render: (_: any, row: Task) => {
          // 同步任务显示源 → 目标
          if (row.kind === "schema_sync" || row.kind === "data_sync") {
            const src = `${row.target_database}.${row.target_schema}.${row.target_table}`;
            const dest = `${row.dest_database}.${row.dest_schema}.${row.dest_table}`;
            return (
              <span style={{ fontSize: 12 }}>
                {src} → {dest}
              </span>
            );
          }
          // 导出任务显示原有逻辑
          if (row.scope === "connection") return <Typography.Text type="secondary">全部数据库</Typography.Text>;
          if (row.scope === "database")
            return <span>{row.target_database}</span>;
          return (
            <span>
              {row.target_database}
              {row.target_schema ? `.${row.target_schema}` : ""}
              {row.target_table ? `.${row.target_table}` : ""}
            </span>
          );
        },
      },
      {
        title: "连接组",
        dataIndex: "group_id",
        width: 140,
        render: (gid: number) => {
          const g = groups.find((x) => x.id === gid);
          return g ? g.name : `#${gid}`;
        },
      },
      {
        title: "状态",
        dataIndex: "status",
        width: 100,
        render: (s: TaskStatus) => (
          <Tag color={STATUS_COLOR[s] ?? "default"}>{STATUS_LABEL[s] ?? s}</Tag>
        ),
      },
      {
        title: "进度",
        dataIndex: "progress",
        width: 160,
        render: (p: number, row: Task) => {
          const status: any =
            row.status === "failed"
              ? "exception"
              : row.status === "succeeded"
              ? "success"
              : row.status === "running"
              ? "active"
              : "normal";
          return (
            <Tooltip
              title={
                row.total_tables
                  ? `${row.done_tables}/${row.total_tables} 表 · ${row.processed_rows} 行`
                  : `${row.processed_rows} 行`
              }
            >
              <Progress percent={p ?? 0} size="small" status={status} />
            </Tooltip>
          );
        },
      },
      { title: "创建人", dataIndex: "creator_name", width: 120, ellipsis: true },
      {
        title: "创建时间",
        dataIndex: "created_at",
        width: 160,
        render: (s: string) => dayjs(s).format("YYYY-MM-DD HH:mm:ss"),
      },
      {
        title: "操作",
        width: 200,
        fixed: "right",
        render: (_: any, row: Task) => (
          <Space size={4}>
            {row.status === "succeeded" && row.file_path && (
              <Tooltip title="下载">
                <Button
                  size="small"
                  type="link"
                  icon={<DownloadOutlined />}
                  onClick={() => handleDownload(row.id)}
                />
              </Tooltip>
            )}
            {(row.status === "pending" || row.status === "running") && (
              <Popconfirm title="确认取消这个任务？" onConfirm={() => handleCancel(row.id)}>
                <Button size="small" type="link" danger icon={<StopOutlined />} />
              </Popconfirm>
            )}
            {row.status !== "running" && (
              <Popconfirm title="确认删除任务记录？" onConfirm={() => handleDelete(row.id)}>
                <Button size="small" type="link" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            )}
          </Space>
        ),
      },
    ],
    [groups]
  );

  return (
    <>
      <Drawer
        open={open}
        onClose={onClose}
        title="异步任务"
        placement="right"
        width="85vw"
        destroyOnClose
      >
        <Space wrap style={{ marginBottom: 12 }}>
          <DatePicker.RangePicker
            showTime
            value={range}
            onChange={(v) => {
              if (v && v[0] && v[1]) {
                setRange([v[0], v[1]]);
                setPage(1);
              }
            }}
            presets={[
              { label: "近 1 天", value: [dayjs().subtract(1, "day"), dayjs()] },
              { label: "近 7 天", value: [dayjs().subtract(7, "day"), dayjs()] },
              { label: "近 30 天", value: [dayjs().subtract(30, "day"), dayjs()] },
            ]}
          />
          <Select
            placeholder="连接组"
            style={{ minWidth: 180 }}
            allowClear
            value={groupID}
            onChange={(v) => {
              setGroupID(v);
              setPage(1);
            }}
            options={groupOptions}
          />
          <Select
            placeholder="类型"
            style={{ minWidth: 100 }}
            allowClear
            value={kind}
            onChange={(v) => {
              setKind(v);
              setPage(1);
            }}
            options={[
              { label: "导出", value: "export" },
              { label: "导入", value: "import" },
            ]}
          />
          <Select
            placeholder="范围"
            style={{ minWidth: 110 }}
            allowClear
            value={scope}
            onChange={(v) => {
              setScope(v);
              setPage(1);
            }}
            options={[
              { label: "整连接", value: "connection" },
              { label: "整库", value: "database" },
              { label: "单表", value: "table" },
            ]}
          />
          <Select
            placeholder="状态"
            style={{ minWidth: 110 }}
            allowClear
            value={status}
            onChange={(v) => {
              setStatus(v);
              setPage(1);
            }}
            options={[
              { label: "排队中", value: "pending" },
              { label: "执行中", value: "running" },
              { label: "成功", value: "succeeded" },
              { label: "失败", value: "failed" },
              { label: "已取消", value: "canceled" },
            ]}
          />
          <Input.Search
            placeholder="创建人 / 库 / 表 / 错误关键字"
            style={{ width: 240 }}
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            onSearch={(v) => {
              setCommittedKeyword(v);
              setPage(1);
            }}
            allowClear
          />
          <Button icon={<ReloadOutlined />} onClick={reload} loading={loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            新建导出
          </Button>
          <Button type="default" icon={<PlusOutlined />} onClick={() => setSyncOpen(true)}>
            新建同步
          </Button>
        </Space>

        <Table<Task>
          size="small"
          rowKey="id"
          loading={loading}
          columns={columns}
          dataSource={data?.items ?? []}
          scroll={{ x: 1300 }}
          pagination={{
            current: data?.page ?? page,
            pageSize: data?.size ?? pageSize,
            total: data?.total ?? 0,
            showSizeChanger: true,
            pageSizeOptions: [10, 20, 50, 100],
            showTotal: (n) => `共 ${n} 条`,
            onChange: (p, s) => {
              setPage(p);
              setPageSize(s);
            },
          }}
        />
      </Drawer>

      <TaskCreateModal
        open={createOpen}
        groups={groups}
        onClose={() => setCreateOpen(false)}
        onCreated={() => {
          setCreateOpen(false);
          reload();
        }}
      />

      <TaskSyncModal
        open={syncOpen}
        groups={groups}
        onClose={() => setSyncOpen(false)}
        onCreated={() => {
          setSyncOpen(false);
          reload();
        }}
      />
    </>
  );
}
