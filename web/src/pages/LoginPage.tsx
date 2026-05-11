import { useState } from "react";
import { App, Button, Card, Form, Input, Tabs } from "antd";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../store";

export default function LoginPage() {
  const { login, register } = useAuth();
  const { message } = App.useApp();
  const nav = useNavigate();
  const [loading, setLoading] = useState(false);

  const onLogin = async (v: { email: string; password: string }) => {
    setLoading(true);
    try {
      await login(v.email, v.password);
      message.success("登录成功");
      nav("/", { replace: true });
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "登录失败");
    } finally {
      setLoading(false);
    }
  };

  const onRegister = async (v: { email: string; name: string; password: string }) => {
    setLoading(true);
    try {
      await register(v.email, v.name, v.password);
      message.success("注册并登录成功");
      nav("/", { replace: true });
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "注册失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        height: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "linear-gradient(135deg, #1f2430 0%, #3b4356 100%)",
      }}
    >
      <Card style={{ width: 420 }} title="Chat2DB Web">
        <Tabs
          items={[
            {
              key: "login",
              label: "登录",
              children: (
                <Form layout="vertical" onFinish={onLogin} disabled={loading}>
                  <Form.Item name="email" label="邮箱" rules={[{ required: true, type: "email" }]}>
                    <Input autoComplete="email" />
                  </Form.Item>
                  <Form.Item name="password" label="密码" rules={[{ required: true, min: 6 }]}>
                    <Input.Password autoComplete="current-password" />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block loading={loading}>
                    登录
                  </Button>
                </Form>
              ),
            },
            {
              key: "register",
              label: "注册",
              children: (
                <Form layout="vertical" onFinish={onRegister} disabled={loading}>
                  <Form.Item name="name" label="昵称" rules={[{ required: true }]}>
                    <Input />
                  </Form.Item>
                  <Form.Item name="email" label="邮箱" rules={[{ required: true, type: "email" }]}>
                    <Input autoComplete="email" />
                  </Form.Item>
                  <Form.Item name="password" label="密码" rules={[{ required: true, min: 6 }]}>
                    <Input.Password autoComplete="new-password" />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" block loading={loading}>
                    注册并登录
                  </Button>
                </Form>
              ),
            },
          ]}
        />
      </Card>
    </div>
  );
}
