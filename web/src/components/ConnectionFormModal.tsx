import { useEffect, useState } from "react";
import { App, Button, Form, Input, InputNumber, Modal, Select, Space } from "antd";
import { api } from "../api";
import type { Connection } from "../types";

interface Props {
  open: boolean;
  groupID?: number;
  editing?: Connection;
  onClose: () => void;
  onSaved: () => void;
}

export default function ConnectionFormModal({ open, groupID, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    if (!open) return;
    if (editing) {
      form.setFieldsValue({
        name: editing.name,
        driver: editing.driver,
        host: editing.host,
        port: editing.port,
        database: editing.database,
        username: editing.username,
        ssl_mode: editing.ssl_mode,
      });
    } else {
      form.resetFields();
      form.setFieldsValue({
        driver: "postgres",
        port: 5432,
        ssl_mode: "disable",
      });
    }
  }, [open, editing, form]);

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      if (editing) {
        await api.updateConnection(editing.id, v);
        message.success("已保存");
      } else if (groupID) {
        await api.createConnection(groupID, v);
        message.success("已创建");
      }
      onSaved();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    const v = await form.validateFields().catch(() => null);
    if (!v) return;
    setTesting(true);
    try {
      let res;
      if (editing && !v.password) {
        res = await api.testConnection({ connection_id: editing.id });
      } else {
        res = await api.testConnection({ draft: v });
      }
      if (res.ok) message.success("连接成功");
      else message.error("连接失败：" + res.error);
    } finally {
      setTesting(false);
    }
  };

  return (
    <Modal
      open={open}
      title={editing ? "编辑连接" : "新建连接"}
      onCancel={onClose}
      width={640}
      footer={
        <Space>
          <Button onClick={test} loading={testing}>
            测试连接
          </Button>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" onClick={submit} loading={saving}>
            保存
          </Button>
        </Space>
      }
      destroyOnClose
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="如：prod-main-db" />
        </Form.Item>
        <Space.Compact block>
          <Form.Item name="driver" label="驱动" style={{ width: "30%" }} rules={[{ required: true }]}>
            <Select
              options={[{ label: "PostgreSQL", value: "postgres" }]}
              disabled
            />
          </Form.Item>
          <Form.Item name="host" label="主机" style={{ width: "50%" }} rules={[{ required: true }]}>
            <Input placeholder="localhost" />
          </Form.Item>
          <Form.Item name="port" label="端口" style={{ width: "20%" }} rules={[{ required: true }]}>
            <InputNumber style={{ width: "100%" }} min={1} max={65535} />
          </Form.Item>
        </Space.Compact>
        <Space.Compact block>
          <Form.Item name="database" label="数据库" style={{ width: "50%" }} rules={[{ required: true }]}>
            <Input placeholder="postgres" />
          </Form.Item>
          <Form.Item name="ssl_mode" label="SSL 模式" style={{ width: "50%" }}>
            <Select
              options={["disable", "require", "verify-ca", "verify-full"].map((s) => ({
                label: s,
                value: s,
              }))}
            />
          </Form.Item>
        </Space.Compact>
        <Space.Compact block>
          <Form.Item name="username" label="用户名" style={{ width: "50%" }} rules={[{ required: true }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label={editing ? "密码（留空表示不修改）" : "密码"} style={{ width: "50%" }}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Space.Compact>
      </Form>
    </Modal>
  );
}
