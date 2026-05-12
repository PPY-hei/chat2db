import { useEffect, useRef, useState } from "react";
import {
  App,
  Button,
  Card,
  Empty,
  Form,
  Input,
  List,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { TeamOutlined, UserAddOutlined, RobotOutlined } from "@ant-design/icons";
import { api } from "../api";
import type { Group, Member, Role } from "../types";

interface Props {
  open: boolean;
  groups: Group[];
  onClose: () => void;
  onChanged: () => Promise<void>;
}

export default function GroupManageModal({ open, groups, onClose, onChanged }: Props) {
  const { message } = App.useApp();
  const [activeID, setActiveID] = useState<string>("__create__");
  const [members, setMembers] = useState<Member[]>([]);
  const [createForm] = Form.useForm();
  const [memberForm] = Form.useForm();
  const [creating, setCreating] = useState(false);
  const [adding, setAdding] = useState(false);

  const prevOpenRef = useRef(false);

  useEffect(() => {
    // 仅在 Modal 从关闭变为打开时，自动选中第一个组（如果有的话）
    if (open && !prevOpenRef.current) {
      if (groups.length > 0) {
        setActiveID(String(groups[0].id));
      } else {
        setActiveID("__create__");
      }
    }
    prevOpenRef.current = open;
  }, [open, groups]);

  useEffect(() => {
    if (!open) return;
    if (activeID === "__create__") {
      setMembers([]);
      return;
    }
    api.listMembers(Number(activeID)).then(setMembers).catch((e) => {
      message.error(e?.response?.data?.error ?? "加载成员失败");
    });
  }, [open, activeID, message]);

  const submitCreate = async () => {
    const v = await createForm.validateFields();
    setCreating(true);
    try {
      const g = await api.createGroup(v.name, v.description ?? "");
      await onChanged();
      message.success("创建成功");
      createForm.resetFields();
      setActiveID(String(g.id));
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "创建失败");
    } finally {
      setCreating(false);
    }
  };

  const submitAddMember = async () => {
    const v = await memberForm.validateFields();
    setAdding(true);
    try {
      await api.addMember(Number(activeID), v.email, v.role);
      const ms = await api.listMembers(Number(activeID));
      setMembers(ms);
      memberForm.resetFields();
      message.success("已添加 / 更新成员");
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "添加失败");
    } finally {
      setAdding(false);
    }
  };

  const removeMember = async (uid: number) => {
    try {
      await api.removeMember(Number(activeID), uid);
      const ms = await api.listMembers(Number(activeID));
      setMembers(ms);
      message.success("已移除");
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "移除失败");
    }
  };

  const currentGroup = groups.find((g) => String(g.id) === activeID);
  const isOwner = currentGroup?.role === "owner";
  const isEditorOrAbove = currentGroup?.role === "owner" || currentGroup?.role === "editor";

  const toggleShareLLM = async (val: boolean) => {
    if (!currentGroup) return;
    try {
      await api.updateGroup(currentGroup.id, { share_llm: val });
      message.success(val ? "已开启大模型配置共享" : "已关闭大模型配置共享");
      await onChanged();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "操作失败");
    }
  };

  return (
    <Modal
      open={open}
      title={
        <Space>
          <TeamOutlined />
          连接组管理
        </Space>
      }
      width={780}
      onCancel={onClose}
      footer={null}
    >
      <Tabs
        tabPosition="left"
        activeKey={activeID}
        onChange={setActiveID}
        style={{ minHeight: 360 }}
        items={[
          ...groups.map((g) => ({
            key: String(g.id),
            label: (
              <Space>
                <span>{g.name}</span>
                <Tag color={g.role === "owner" ? "orange" : g.role === "editor" ? "green" : "default"}>
                  {g.role}
                </Tag>
              </Space>
            ),
            children: (
              <div>
                <Card size="small" style={{ marginBottom: 16 }}>
                  <Typography.Paragraph style={{ marginBottom: 4 }}>
                    <strong>{g.name}</strong>
                  </Typography.Paragraph>
                  <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                    {g.description || "无描述"} · {g.member_count} 名成员
                  </Typography.Paragraph>
                </Card>

                {isOwner && (
                  <Card
                    size="small"
                    title={
                      <Space>
                        <RobotOutlined />
                        AI 大模型配置共享
                      </Space>
                    }
                    style={{ marginBottom: 16 }}
                    extra={
                      <Tooltip title="开启后，组内未自行配置 LLM 的成员将自动使用你的 endpoint / model / api key（仅在服务端代理调用时使用，不会下发给前端）">
                        <Switch
                          checked={!!g.share_llm}
                          checkedChildren="已共享"
                          unCheckedChildren="未共享"
                          onChange={toggleShareLLM}
                        />
                      </Tooltip>
                    }
                  >
                    <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                      {g.share_llm
                        ? "组内成员若自身未配置大模型，会自动使用你的 LLM 凭据。Api Key 始终保留在服务端，不会泄露给成员。"
                        : "成员需各自配置 LLM。开启共享后，没有自己 LLM 配置的成员将复用你的设置。"}
                    </Typography.Paragraph>
                  </Card>
                )}

                {isEditorOrAbove ? (
                  <Card size="small" title="邀请 / 更新成员" style={{ marginBottom: 16 }}>
                    <Form form={memberForm} layout="inline" preserve={false}>
                      <Form.Item name="email" rules={[{ required: true, type: "email" }]}>
                        <Input placeholder="对方邮箱" autoComplete="off" onPressEnter={submitAddMember} />
                      </Form.Item>
                      <Form.Item name="role" rules={[{ required: true }]} initialValue="viewer">
                        <Select
                          style={{ width: 120 }}
                          options={
                            isOwner
                              ? [
                                  { label: "Owner", value: "owner" },
                                  { label: "Editor", value: "editor" },
                                  { label: "Viewer", value: "viewer" },
                                ]
                              : [
                                  { label: "Editor", value: "editor" },
                                  { label: "Viewer", value: "viewer" },
                                ]
                          }
                        />
                      </Form.Item>
                      <Button type="primary" icon={<UserAddOutlined />} loading={adding} onClick={submitAddMember}>
                        {isOwner ? "添加 / 更新" : "邀请"}
                      </Button>
                    </Form>
                    {!isOwner && (
                      <Typography.Paragraph type="secondary" style={{ marginBottom: 0, marginTop: 8 }}>
                        Editor 仅可邀请新成员（角色：Viewer / Editor），不能修改既有成员角色或邀请 Owner。
                      </Typography.Paragraph>
                    )}
                  </Card>
                ) : (
                  <Typography.Paragraph type="secondary">
                    仅 Owner 或 Editor 可以邀请成员。
                  </Typography.Paragraph>
                )}

                <List
                  size="small"
                  bordered
                  header={<strong>成员列表</strong>}
                  dataSource={members}
                  locale={{ emptyText: <Empty description="无成员" /> }}
                  renderItem={(m) => (
                    <List.Item
                      actions={
                        isOwner
                          ? [
                              <Popconfirm
                                key="del"
                                title="确认移除该成员？"
                                onConfirm={() => removeMember(m.user_id)}
                              >
                                <Button danger size="small" type="link">
                                  移除
                                </Button>
                              </Popconfirm>,
                            ]
                          : []
                      }
                    >
                      <Space size={12}>
                        <strong>{m.name}</strong>
                        <Typography.Text type="secondary">{m.email}</Typography.Text>
                        <Tag color={m.role === "owner" ? "orange" : m.role === "editor" ? "green" : "default"}>
                          {m.role}
                        </Tag>
                      </Space>
                    </List.Item>
                  )}
                />
              </div>
            ),
          })),
          {
            key: "__create__",
            label: <Space>+ 新建组</Space>,
            children: (
              <Form form={createForm} layout="vertical" preserve={false}>
                <Form.Item name="name" label="组名称" rules={[{ required: true }]}>
                  <Input placeholder="如：研发组、运维组" onPressEnter={submitCreate} />
                </Form.Item>
                <Form.Item name="description" label="描述">
                  <Input.TextArea rows={3} placeholder="可选" />
                </Form.Item>
                <Button type="primary" loading={creating} onClick={submitCreate}>
                  创建
                </Button>
              </Form>
            ),
          },
        ]}
      />
    </Modal>
  );
}

export type { Role };
