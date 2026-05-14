import { useCallback, useEffect, useMemo, useState } from "react";
import {
  App,
  Button,
  DatePicker,
  Drawer,
  Input,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import dayjs, { Dayjs } from "dayjs";
import type { ColumnsType } from "antd/es/table";
import { api } from "../api";
import type { AuditAction, AuditLog, AuditLogPage, Group } from "../types";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
}

// action 颜色映射，与项目其它角色 Tag 风格一致。
// 用 Record<AuditAction, string> 让 TS 在 model 端新增 action 时编译期提示漏配色。
const ACTION_COLOR: Record<AuditAction, string> = {
  "sql.execute": "geekblue",
  "auth.login.success": "green",
  "auth.login.fail": "red",
  "auth.register": "blue",
  "member.add": "cyan",
  "member.remove": "magenta",
  "member.update": "purple",
  "connection.create": "lime",
  "connection.update": "gold",
  "connection.delete": "volcano",
  "connection.test": "default",
};

const DEFAULT_PAGE_SIZE = 50;

export default function AuditLogModal({ open, groups, onClose }: Props) {
  const { message } = App.useApp();
  // 时间窗口默认近 7 天，用户可改。
  const [range, setRange] = useState<[Dayjs, Dayjs]>(() => [dayjs().subtract(7, "day"), dayjs()]);
  const [actions, setActions] = useState<AuditAction[]>([]);
  const [actionOptions, setActionOptions] = useState<AuditAction[]>([]);
  const [keyword, setKeyword] = useState("");
  // committedKeyword 是真正打到后端的关键字；keyword 仅是输入框 state。
  // 拆开两个 state 避免每次按键都触发后端 LIKE 全表扫描。
  const [committedKeyword, setCommittedKeyword] = useState("");
  const [onlyFail, setOnlyFail] = useState(false);
  const [groupID, setGroupID] = useState<number | undefined>(undefined);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [data, setData] = useState<AuditLogPage | null>(null);
  const [loading, setLoading] = useState(false);

  // 仅展示 admin/owner 可见的组（后端 visible_group_ids 二次确认）。
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
      const resp = await api.listAuditLogs({
        from: range[0].toISOString(),
        to: range[1].toISOString(),
        actions: actions.length > 0 ? actions : undefined,
        keyword: committedKeyword || undefined,
        only_fail: onlyFail,
        page,
        size: pageSize,
        group_id: groupID,
      });
      setData(resp);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载审计日志失败");
    } finally {
      setLoading(false);
    }
  }, [open, range, actions, committedKeyword, onlyFail, page, pageSize, groupID, message]);

  // 打开抽屉时拉一次 actions 枚举，单独的请求避免阻塞主查询。
  useEffect(() => {
    if (!open) return;
    api.listAuditActions().then(setActionOptions).catch(() => {});
  }, [open]);

  useEffect(() => {
    reload();
  }, [reload]);

  const columns: ColumnsType<AuditLog> = useMemo(
    () => [
      {
        title: "时间",
        dataIndex: "created_at",
        width: 170,
        render: (s: string) => dayjs(s).format("YYYY-MM-DD HH:mm:ss"),
      },
      {
        title: "用户",
        dataIndex: "user_email",
        width: 200,
        ellipsis: true,
      },
      {
        title: "操作",
        dataIndex: "action",
        width: 180,
        render: (a: AuditAction) => <Tag color={ACTION_COLOR[a] ?? "default"}>{a}</Tag>,
      },
      {
        title: "结果",
        dataIndex: "success",
        width: 80,
        render: (ok: boolean) =>
          ok ? <Tag color="green">成功</Tag> : <Tag color="red">失败</Tag>,
      },
      {
        title: "对象",
        dataIndex: "target",
        ellipsis: true,
      },
      {
        title: "组",
        dataIndex: "group_id",
        width: 120,
        render: (gid?: number) => {
          if (!gid) return <Typography.Text type="secondary">-</Typography.Text>;
          const g = groups.find((x) => x.id === gid);
          return g ? g.name : `#${gid}`;
        },
      },
      {
        title: "耗时",
        dataIndex: "duration_ms",
        width: 90,
        render: (ms: number) => `${ms} ms`,
      },
      {
        title: "客户端",
        dataIndex: "ip",
        width: 140,
        ellipsis: true,
        render: (ip: string, row: AuditLog) => (
          <Tooltip title={row.user_agent || "-"}>{ip || "-"}</Tooltip>
        ),
      },
      {
        title: "错误",
        dataIndex: "error_msg",
        ellipsis: true,
      },
    ],
    [groups]
  );

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title="审计日志"
      placement="right"
      width="80vw"
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
            { label: "近 1 小时", value: [dayjs().subtract(1, "hour"), dayjs()] },
            { label: "近 24 小时", value: [dayjs().subtract(1, "day"), dayjs()] },
            { label: "近 7 天", value: [dayjs().subtract(7, "day"), dayjs()] },
            { label: "近 30 天", value: [dayjs().subtract(30, "day"), dayjs()] },
          ]}
        />
        <Select
          mode="multiple"
          placeholder="操作类型"
          style={{ minWidth: 220 }}
          allowClear
          value={actions}
          onChange={(v) => {
            setActions(v);
            setPage(1);
          }}
          options={actionOptions.map((a) => ({ label: a, value: a }))}
        />
        <Select
          placeholder="组"
          style={{ minWidth: 160 }}
          allowClear
          value={groupID}
          onChange={(v) => {
            setGroupID(v);
            setPage(1);
          }}
          options={groupOptions}
        />
        <Input.Search
          placeholder="邮箱 / 对象 / 详情关键词，回车搜索"
          style={{ width: 240 }}
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          onSearch={(v) => {
            setCommittedKeyword(v);
            setPage(1);
          }}
          allowClear
        />
        <Space size={4}>
          仅失败
          <Switch
            checked={onlyFail}
            onChange={(v) => {
              setOnlyFail(v);
              setPage(1);
            }}
          />
        </Space>
        <Button icon={<ReloadOutlined />} onClick={reload} loading={loading}>
          刷新
        </Button>
      </Space>

      <Table<AuditLog>
        size="small"
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={data?.items ?? []}
        expandable={{
          expandedRowRender: (row) => (
            <pre style={{ whiteSpace: "pre-wrap", margin: 0, fontSize: 12 }}>
              {prettyJSON(row.detail)}
            </pre>
          ),
          rowExpandable: (row) => row.detail !== "" && row.detail !== "{}",
        }}
        pagination={{
          current: data?.page ?? page,
          pageSize: data?.size ?? pageSize,
          total: data?.total ?? 0,
          showSizeChanger: true,
          pageSizeOptions: [20, 50, 100, 200],
          showTotal: (n) => `共 ${n} 条`,
          onChange: (p, s) => {
            setPage(p);
            setPageSize(s);
          },
        }}
      />
    </Drawer>
  );
}

function prettyJSON(s: string): string {
  if (!s) return "";
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
