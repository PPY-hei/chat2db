import { useEffect, useState } from "react";
import {
  App,
  Button,
  Collapse,
  Form,
  Input,
  InputNumber,
  Modal,
  Radio,
  Select,
  Space,
  Switch,
  Typography,
  Upload,
} from "antd";
import { UploadOutlined } from "@ant-design/icons";
import { api } from "../api";
import type { Connection } from "../types";

interface Props {
  open: boolean;
  groupID?: number;
  editing?: Connection;
  onClose: () => void;
  onSaved: () => void;
}

// 从 Upload 的 file 对象读取文本内容（PEM 文件）
function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = reject;
    reader.readAsText(file);
  });
}

export default function ConnectionFormModal({ open, groupID, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [sshEnabled, setSSHEnabled] = useState(false);
  const [sshAuthMethod, setSSHAuthMethod] = useState<string>("password");
  const [sslMode, setSSLMode] = useState<string>("disable");
  const [driver, setDriver] = useState<string>("postgres");
  // 记住 PG 的 sslMode，切到 MySQL 再切回时恢复
  const [pgSSLMode, setPgSSLMode] = useState<string>("disable");

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
        ssh_enabled: editing.ssh_enabled,
        ssh_host: editing.ssh_host,
        ssh_port: editing.ssh_port || 22,
        ssh_user: editing.ssh_user,
        ssh_auth_method: editing.ssh_auth_method || "password",
      });
      setSSHEnabled(!!editing.ssh_enabled);
      setSSHAuthMethod(editing.ssh_auth_method || "password");
      setSSLMode(editing.ssl_mode || "disable");
      setDriver(editing.driver || "postgres");
    } else {
      form.resetFields();
      form.setFieldsValue({
        driver: "postgres",
        port: 5432,
        ssl_mode: "disable",
        ssh_enabled: false,
        ssh_port: 22,
        ssh_auth_method: "password",
      });
      setSSHEnabled(false);
      setSSHAuthMethod("password");
      setSSLMode("disable");
      setDriver("postgres");
    }
  }, [open, editing, form]);

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      // 处理文件上传字段：把 Upload 的 fileList 转成 PEM 文本
      const payload: any = { ...v };
      if (v.ssl_ca_cert_file?.[0]?.originFileObj) {
        payload.ssl_ca_cert = await readFileAsText(v.ssl_ca_cert_file[0].originFileObj);
      }
      delete payload.ssl_ca_cert_file;
      if (v.ssl_client_cert_file?.[0]?.originFileObj) {
        payload.ssl_client_cert = await readFileAsText(v.ssl_client_cert_file[0].originFileObj);
      }
      delete payload.ssl_client_cert_file;
      if (v.ssl_client_key_file?.[0]?.originFileObj) {
        payload.ssl_client_key = await readFileAsText(v.ssl_client_key_file[0].originFileObj);
      }
      delete payload.ssl_client_key_file;
      if (v.ssh_private_key_file?.[0]?.originFileObj) {
        payload.ssh_private_key = await readFileAsText(v.ssh_private_key_file[0].originFileObj);
      }
      delete payload.ssh_private_key_file;

      if (editing) {
        await api.updateConnection(editing.id, payload);
        message.success("已保存");
      } else if (groupID) {
        await api.createConnection(groupID, payload);
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
        // 处理文件
        const payload: any = { ...v };
        if (v.ssl_ca_cert_file?.[0]?.originFileObj) {
          payload.ssl_ca_cert = await readFileAsText(v.ssl_ca_cert_file[0].originFileObj);
        }
        delete payload.ssl_ca_cert_file;
        if (v.ssl_client_cert_file?.[0]?.originFileObj) {
          payload.ssl_client_cert = await readFileAsText(v.ssl_client_cert_file[0].originFileObj);
        }
        delete payload.ssl_client_cert_file;
        if (v.ssl_client_key_file?.[0]?.originFileObj) {
          payload.ssl_client_key = await readFileAsText(v.ssl_client_key_file[0].originFileObj);
        }
        delete payload.ssl_client_key_file;
        if (v.ssh_private_key_file?.[0]?.originFileObj) {
          payload.ssh_private_key = await readFileAsText(v.ssh_private_key_file[0].originFileObj);
        }
        delete payload.ssh_private_key_file;
        res = await api.testConnection({ draft: payload });
      }
      if (res.ok) message.success("连接成功");
      else message.error("连接失败：" + res.error);
    } finally {
      setTesting(false);
    }
  };

  const needCerts = sslMode === "verify-ca" || sslMode === "verify-full";

  return (
    <Modal
      open={open}
      title={editing ? "编辑连接" : "新建连接"}
      onCancel={onClose}
      width={680}
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
      <Form form={form} layout="vertical" size="small">
        {/* 基础连接信息 */}
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="如：prod-main-db" />
        </Form.Item>
        <Space.Compact block>
          <Form.Item name="driver" label="驱动" style={{ width: "30%" }} rules={[{ required: true }]}>
            <Select
              options={[
                { label: "PostgreSQL", value: "postgres" },
                { label: "MySQL", value: "mysql" },
              ]}
              onChange={(v: string) => {
                const prevDriver = driver;
                setDriver(v);
                const defaultPort = v === "mysql" ? 3306 : 5432;
                form.setFieldsValue({ port: defaultPort });
                if (v === "mysql") {
                  // 保存当前 PG 的 sslMode，切到 MySQL 时强制 disable
                  if (prevDriver === "postgres") {
                    setPgSSLMode(sslMode);
                  }
                  form.setFieldsValue({ ssl_mode: "disable" });
                  setSSLMode("disable");
                } else if (v === "postgres" && prevDriver === "mysql") {
                  // 切回 PG 时恢复之前的 sslMode
                  form.setFieldsValue({ ssl_mode: pgSSLMode });
                  setSSLMode(pgSSLMode);
                }
              }}
            />
          </Form.Item>
          <Form.Item name="host" label="主机" style={{ width: "50%" }} rules={[{ required: true }]}>
            <Input placeholder="localhost 或 远程 IP" />
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
              options={
                driver === "mysql"
                  ? [
                      { label: "disable", value: "disable" },
                      { label: "require (skip-verify)", value: "require" },
                      { label: "verify-ca", value: "verify-ca" },
                    ]
                  : ["disable", "allow", "prefer", "require", "verify-ca", "verify-full"].map((s) => ({
                      label: s,
                      value: s,
                    }))
              }
              onChange={(v) => setSSLMode(v)}
            />
          </Form.Item>
        </Space.Compact>
        <Space.Compact block>
          <Form.Item name="username" label="用户名" style={{ width: "50%" }} rules={[{ required: true }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label={editing ? "密码（留空不修改）" : "密码"} style={{ width: "50%" }}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Space.Compact>

        {/* SSL 证书 */}
        <Collapse
          ghost
          size="small"
          style={{ marginBottom: 16 }}
          items={[
            {
              key: "ssl",
              label: (
                <Typography.Text strong>
                  SSL 证书 {needCerts && <Typography.Text type="warning">(当前模式需要)</Typography.Text>}
                </Typography.Text>
              ),
              children: (
                <>
                  <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
                    上传 PEM 格式证书文件（适用于 verify-ca / verify-full 等需要证书的模式）。
                  </Typography.Paragraph>
                  <Form.Item
                    name="ssl_ca_cert_file"
                    label="CA 根证书 (ca.pem / server-ca.pem)"
                    valuePropName="fileList"
                    getValueFromEvent={(e) => (Array.isArray(e) ? e : e?.fileList)}
                  >
                    <Upload beforeUpload={() => false} maxCount={1} accept=".pem,.crt,.cer">
                      <Button icon={<UploadOutlined />}>选择文件</Button>
                    </Upload>
                  </Form.Item>
                  <Form.Item
                    name="ssl_client_cert_file"
                    label="客户端证书 (client-cert.pem)"
                    valuePropName="fileList"
                    getValueFromEvent={(e) => (Array.isArray(e) ? e : e?.fileList)}
                  >
                    <Upload beforeUpload={() => false} maxCount={1} accept=".pem,.crt,.cer">
                      <Button icon={<UploadOutlined />}>选择文件</Button>
                    </Upload>
                  </Form.Item>
                  <Form.Item
                    name="ssl_client_key_file"
                    label="客户端私钥 (client-key.pem)"
                    valuePropName="fileList"
                    getValueFromEvent={(e) => (Array.isArray(e) ? e : e?.fileList)}
                  >
                    <Upload beforeUpload={() => false} maxCount={1} accept=".pem,.key">
                      <Button icon={<UploadOutlined />}>选择文件</Button>
                    </Upload>
                  </Form.Item>
                </>
              ),
            },
          ]}
        />

        {/* SSH 隧道 */}
        <Collapse
          ghost
          size="small"
          style={{ marginBottom: 16 }}
          items={[
            {
              key: "ssh",
              label: (
                <Typography.Text strong>
                  SSH 隧道 {sshEnabled && <Typography.Text type="success">(已启用)</Typography.Text>}
                </Typography.Text>
              ),
              children: (
                <>
                  <Form.Item name="ssh_enabled" label="启用 SSH 隧道" valuePropName="checked">
                    <Switch
                      checkedChildren="开"
                      unCheckedChildren="关"
                      onChange={(v) => setSSHEnabled(v)}
                    />
                  </Form.Item>
                  {sshEnabled && (
                    <>
                      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
                        通过 SSH 跳板机建立隧道连接到数据库（堡垒机/跳板机场景）。
                        上方的主机地址应填写 <b>数据库从 SSH 服务器视角</b> 可达的地址（通常是内网 IP 或 localhost）。
                      </Typography.Paragraph>
                      <Space.Compact block>
                        <Form.Item
                          name="ssh_host"
                          label="SSH 主机"
                          style={{ width: "50%" }}
                          rules={[{ required: sshEnabled }]}
                        >
                          <Input placeholder="跳板机 IP / 域名" />
                        </Form.Item>
                        <Form.Item name="ssh_port" label="SSH 端口" style={{ width: "20%" }}>
                          <InputNumber style={{ width: "100%" }} min={1} max={65535} />
                        </Form.Item>
                        <Form.Item
                          name="ssh_user"
                          label="SSH 用户"
                          style={{ width: "30%" }}
                          rules={[{ required: sshEnabled }]}
                        >
                          <Input placeholder="root" autoComplete="off" />
                        </Form.Item>
                      </Space.Compact>
                      <Form.Item name="ssh_auth_method" label="认证方式">
                        <Radio.Group onChange={(e) => setSSHAuthMethod(e.target.value)}>
                          <Radio value="password">密码</Radio>
                          <Radio value="privatekey">私钥</Radio>
                        </Radio.Group>
                      </Form.Item>
                      {sshAuthMethod === "password" ? (
                        <Form.Item name="ssh_password" label="SSH 密码">
                          <Input.Password autoComplete="new-password" placeholder="SSH 登录密码" />
                        </Form.Item>
                      ) : (
                        <>
                          <Form.Item
                            name="ssh_private_key_file"
                            label="SSH 私钥文件 (id_rsa / id_ed25519)"
                            valuePropName="fileList"
                            getValueFromEvent={(e) => (Array.isArray(e) ? e : e?.fileList)}
                          >
                            <Upload beforeUpload={() => false} maxCount={1} accept=".pem,.key,">
                              <Button icon={<UploadOutlined />}>选择私钥文件</Button>
                            </Upload>
                          </Form.Item>
                          <Form.Item name="ssh_passphrase" label="私钥 Passphrase（如果有）">
                            <Input.Password autoComplete="new-password" placeholder="留空表示无加密" />
                          </Form.Item>
                        </>
                      )}
                    </>
                  )}
                </>
              ),
            },
          ]}
        />
      </Form>
    </Modal>
  );
}
