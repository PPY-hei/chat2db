import { useEffect, useState } from "react";
import { App, Form, Input, Modal, Typography } from "antd";
import { api } from "../api";

interface Props {
  open: boolean;
  currentEndpoint?: string;
  currentModel?: string;
  onClose: () => void;
  onSaved: () => void;
}

export default function LLMConfigModal({
  open,
  currentEndpoint,
  currentModel,
  onClose,
  onSaved,
}: Props) {
  const { message } = App.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      endpoint: currentEndpoint ?? "https://api.openai.com",
      model: currentModel ?? "gpt-4o-mini",
      api_key: "",
    });
  }, [open, currentEndpoint, currentModel, form]);

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await api.updateLLM(v.endpoint, v.model, v.api_key ?? "");
      message.success("已保存");
      onSaved();
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "保存失败");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title="AI 配置（OpenAI 兼容接口）"
      onCancel={onClose}
      onOk={submit}
      confirmLoading={saving}
      okText="保存"
      destroyOnClose
    >
      <Typography.Paragraph type="secondary">
        填写后，可以在 SQL 编辑器中通过 <kbd>⌥/Alt + I</kbd> 或顶栏按钮唤起 AI 协助编写 SQL。
        支持任何 OpenAI 兼容的 API（如 OpenAI、DeepSeek、Kimi、本地 vLLM 等）。
      </Typography.Paragraph>
      <Form form={form} layout="vertical">
        <Form.Item
          name="endpoint"
          label="Endpoint"
          rules={[{ required: true, message: "必填" }]}
          extra="例如 https://api.openai.com 或 https://api.deepseek.com"
        >
          <Input placeholder="https://api.openai.com" />
        </Form.Item>
        <Form.Item name="model" label="模型" rules={[{ required: true }]}>
          <Input placeholder="gpt-4o-mini / deepseek-chat / qwen-plus ..." />
        </Form.Item>
        <Form.Item name="api_key" label="API Key" extra="留空表示保留原有 key">
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
