import { useEffect, useState } from "react";
import { App, Button, Empty, Input, List, Modal, Popconfirm, Space, Tag, Typography } from "antd";
import { StarFilled, ThunderboltOutlined, DeleteOutlined } from "@ant-design/icons";
import { api } from "../api";
import type { SavedQuery } from "../types";

interface Props {
  open: boolean;
  onClose: () => void;
  onJump: (connID: number, sql: string) => void;
}

export default function MySavedQueriesModal({ open, onClose, onJump }: Props) {
  const { message } = App.useApp();
  const [rows, setRows] = useState<SavedQuery[]>([]);
  const [kw, setKw] = useState("");
  const [loading, setLoading] = useState(false);

  const refresh = async () => {
    setLoading(true);
    try {
      const r = await api.listMySavedQueries();
      setRows(r);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open) refresh();
  }, [open]);

  const filtered = rows.filter((r) => {
    if (!kw) return true;
    const s = kw.toLowerCase();
    return [r.title, r.description, r.group_name, r.connection_name, r.database, r.created_by_name]
      .filter(Boolean)
      .some((x) => String(x).toLowerCase().includes(s));
  });

  return (
    <Modal
      open={open}
      title={
        <Space>
          <StarFilled style={{ color: "#f59e0b" }} />
          我的收藏 SQL
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            展示你所在任意组内的共享收藏
          </Typography.Text>
        </Space>
      }
      width={820}
      footer={null}
      onCancel={onClose}
    >
      <Input.Search
        placeholder="搜索标题、描述、组名、连接名、数据库、作者..."
        value={kw}
        onChange={(e) => setKw(e.target.value)}
        style={{ marginBottom: 12 }}
        allowClear
      />
      <List
        loading={loading}
        dataSource={filtered}
        locale={{ emptyText: <Empty description="暂无收藏" /> }}
        renderItem={(r) => (
          <List.Item
            key={r.id}
            actions={[
              <Button
                key="jump"
                type="primary"
                size="small"
                icon={<ThunderboltOutlined />}
                onClick={() => onJump(r.connection_id, r.sql)}
              >
                去执行
              </Button>,
              <Popconfirm
                key="del"
                title="删除该收藏？"
                onConfirm={async () => {
                  try {
                    await api.deleteSavedQuery(r.id);
                    await refresh();
                  } catch (e: any) {
                    message.error(e?.response?.data?.error ?? "删除失败");
                  }
                }}
              >
                <Button size="small" icon={<DeleteOutlined />} danger type="link">
                  删除
                </Button>
              </Popconfirm>,
            ]}
          >
            <List.Item.Meta
              title={
                <Space size={8}>
                  <strong>{r.title}</strong>
                  <Tag color="blue">{r.group_name}</Tag>
                  <Tag color="geekblue">
                    {r.connection_name} / {r.database}
                  </Tag>
                </Space>
              }
              description={
                <div>
                  {r.description && <div style={{ color: "#4b5563", marginBottom: 4 }}>{r.description}</div>}
                  <pre
                    style={{
                      background: "#f5f6f8",
                      padding: 8,
                      borderRadius: 4,
                      fontSize: 12,
                      maxHeight: 160,
                      overflow: "auto",
                      margin: 0,
                    }}
                  >
                    {r.sql}
                  </pre>
                  <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                    作者：{r.created_by_name}
                  </Typography.Text>
                </div>
              }
            />
          </List.Item>
        )}
      />
    </Modal>
  );
}
