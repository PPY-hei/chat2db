import { useEffect, useMemo, useState } from "react";
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
import type { Connection, DriverInfo } from "../types";

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

// 给已知驱动一个友好 label；未知驱动回落到 name，保证后端新增驱动时前端自动识别。
const DRIVER_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  mysql: "MySQL",
};
function driverLabel(name: string): string {
  return DRIVER_LABELS[name] ?? name;
}

export default function ConnectionFormModal({ open, groupID, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [sshEnabled, setSSHEnabled] = useState(false);
  const [sshAuthMethod, setSSHAuthMethod] = useState<string>("password");
  const [proxyEnabled, setProxyEnabled] = useState(false);
  const [proxyType, setProxyType] = useState<string>("http");
  const [sslMode, setSSLMode] = useState<string>("disable");
  const [driver, setDriver] = useState<string>("postgres");
  // 每个 driver 独立记忆 sslMode，切回时恢复（通用于任意驱动，不再只对 PG 特化）
  const [sslModeByDriver, setSSLModeByDriver] = useState<Record<string, string>>({});

  // 可用驱动列表来自后端 GET /api/drivers，真相源收敛在 dbexec registry
  const [drivers, setDrivers] = useState<DriverInfo[]>([]);
  const [driversLoading, setDriversLoading] = useState(false);

  const driversByName = useMemo(() => {
    const m: Record<string, DriverInfo> = {};
    for (const d of drivers) m[d.name] = d;
    return m;
  }, [drivers]);

  // 打开对话框时拉取驱动列表（打开一次缓存一次即可；如需强刷改成 open 依赖）
  useEffect(() => {
    if (!open || drivers.length > 0) return;
    setDriversLoading(true);
    api
      .listDrivers()
      .then(setDrivers)
      .catch((e: any) => message.error("加载驱动列表失败：" + (e?.message ?? "unknown")))
      .finally(() => setDriversLoading(false));
  }, [open, drivers.length, message]);

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
        proxy_enabled: editing.proxy_enabled,
        proxy_type: editing.proxy_type || "http",
        proxy_host: editing.proxy_host,
        proxy_port: editing.proxy_port || undefined,
        proxy_username: editing.proxy_username,
      });
      setSSHEnabled(!!editing.ssh_enabled);
      setSSHAuthMethod(editing.ssh_auth_method || "password");
      setProxyEnabled(!!editing.proxy_enabled);
      setProxyType(editing.proxy_type || "http");
      setSSLMode(editing.ssl_mode || "disable");
      setDriver(editing.driver || "");
    } else {
      // 新建：等 drivers 加载完后再填默认值，避免先渲染空表单又被覆盖
      const firstDriver = drivers[0];
      form.resetFields();
      form.setFieldsValue({
        driver: firstDriver?.name ?? "",
        port: firstDriver?.default_port ?? undefined,
        ssl_mode: firstDriver?.ssl_modes?.[0] ?? "disable",
        ssh_enabled: false,
        ssh_port: 22,
        ssh_auth_method: "password",
        proxy_enabled: false,
        proxy_type: "http",
      });
      setSSHEnabled(false);
      setSSHAuthMethod("password");
      setProxyEnabled(false);
      setProxyType("http");
      setSSLMode(firstDriver?.ssl_modes?.[0] ?? "disable");
      setDriver(firstDriver?.name ?? "");
    }
  }, [open, editing, form, drivers]);

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
  const currentDriver = driversByName[driver];
  // 驱动能力：未拿到时回落到"全开"避免误隐藏 UI；拿到后严格按 Capabilities 显隐
  const supportsSSH = currentDriver ? currentDriver.supports_ssh : true;
  const supportsProxy = currentDriver ? currentDriver.supports_proxy : true;
  const supportsMTLS = currentDriver ? currentDriver.supports_mtls : true;
  const sslOptions = currentDriver?.ssl_modes?.length
    ? currentDriver.ssl_modes.map((s) => ({ label: s, value: s }))
    : [{ label: "disable", value: "disable" }];

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
              loading={driversLoading}
              options={drivers.map((d) => ({ label: driverLabel(d.name), value: d.name }))}
              onChange={(v: string) => {
                const prevDriver = driver;
                setDriver(v);
                // 默认端口：来自后端 Capabilities，避免前端硬编码
                const next = driversByName[v];
                if (next?.default_port) {
                  form.setFieldsValue({ port: next.default_port });
                }
                // SSL 模式：保存上一驱动的选择，恢复目标驱动上次的选择；
                // 若目标驱动不再支持当前模式，回落到该驱动的首个模式或 disable。
                setSSLModeByDriver((prev) => ({ ...prev, [prevDriver]: sslMode }));
                const remembered = sslModeByDriver[v];
                const allowed = next?.ssl_modes ?? [];
                let nextSSL = remembered ?? allowed[0] ?? "disable";
                if (allowed.length > 0 && !allowed.includes(nextSSL)) {
                  nextSSL = allowed[0];
                }
                form.setFieldsValue({ ssl_mode: nextSSL });
                setSSLMode(nextSSL);
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
            <Select options={sslOptions} onChange={(v) => setSSLMode(v)} />
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

        {/* SSL 证书（仅 mTLS 驱动展示） */}
        {supportsMTLS && (
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
        )}

        {/* SSH 隧道（仅支持 SSH 的驱动展示） */}
        {supportsSSH && (
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
        )}

        {/* HTTP/SOCKS5 代理（仅支持代理的驱动展示） */}
        {supportsProxy && (
        <Collapse
          ghost
          size="small"
          style={{ marginBottom: 16 }}
          items={[
            {
              key: "proxy",
              label: (
                <Typography.Text strong>
                  代理 {proxyEnabled && <Typography.Text type="success">(已启用)</Typography.Text>}
                </Typography.Text>
              ),
              children: (
                <>
                  <Form.Item name="proxy_enabled" label="启用代理" valuePropName="checked">
                    <Switch
                      checkedChildren="开"
                      unCheckedChildren="关"
                      onChange={(v) => setProxyEnabled(v)}
                    />
                  </Form.Item>
                  {proxyEnabled && (
                    <>
                      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
                        通过 HTTP/SOCKS5 代理中转访问内网数据库。上方主机地址应填写
                        <b>代理服务器视角</b>可达的数据库地址（通常是内网 IP）。
                        代理与 SSH 隧道互斥，二者同时开启时优先走 SSH。
                      </Typography.Paragraph>
                      <Form.Item name="proxy_type" label="代理类型">
                        <Radio.Group onChange={(e) => setProxyType(e.target.value)}>
                          <Radio value="http">HTTP CONNECT</Radio>
                          <Radio value="socks5">SOCKS5</Radio>
                        </Radio.Group>
                      </Form.Item>
                      <Space.Compact block>
                        <Form.Item
                          name="proxy_host"
                          label="代理主机"
                          style={{ width: "70%" }}
                          rules={[{ required: proxyEnabled }]}
                        >
                          <Input placeholder="代理 IP / 域名" />
                        </Form.Item>
                        <Form.Item
                          name="proxy_port"
                          label="代理端口"
                          style={{ width: "30%" }}
                          rules={[{ required: proxyEnabled }]}
                        >
                          <InputNumber style={{ width: "100%" }} min={1} max={65535} />
                        </Form.Item>
                      </Space.Compact>
                      <Space.Compact block>
                        <Form.Item name="proxy_username" label="用户名（可选）" style={{ width: "50%" }}>
                          <Input autoComplete="off" placeholder="留空表示无需认证" />
                        </Form.Item>
                        <Form.Item name="proxy_password" label="密码（可选）" style={{ width: "50%" }}>
                          <Input.Password autoComplete="new-password" placeholder="留空表示无需认证" />
                        </Form.Item>
                      </Space.Compact>
                      <Typography.Text type="warning" style={{ fontSize: 12 }}>
                        {proxyType === "socks5" ? "SOCKS5 代理" : "HTTP CONNECT 代理"}
                      </Typography.Text>
                    </>
                  )}
                </>
              ),
            },
          ]}
        />
        )}
      </Form>
    </Modal>
  );
}
