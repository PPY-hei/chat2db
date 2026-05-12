import { useEffect, useMemo, useRef, useState } from "react";
import {
  App,
  Button,
  Dropdown,
  Empty,
  Form,
  Input,
  Mentions,
  Modal,
  Result,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  CaretRightOutlined,
  RobotOutlined,
  StarOutlined,
  CopyOutlined,
  DownloadOutlined,
  TeamOutlined,
} from "@ant-design/icons";
import Editor, { OnMount } from "@monaco-editor/react";
import type { editor as MonacoEditor } from "monaco-editor";
import { api } from "../api";
import type { ExecuteResponse, QueryResult, SavedQuery } from "../types";
import type { OpenedTab } from "../pages/MainLayout";

interface Props {
  tab: OpenedTab;
}

export default function SQLTab({ tab }: Props) {
  const { message } = App.useApp();
  const editorRef = useRef<MonacoEditor.IStandaloneCodeEditor | null>(null);
  const defaultSQL = tab.driver === "mysql" ? "-- Write SQL here\nSELECT NOW();\n" : "-- Write SQL here\nSELECT now();\n";
  const [sql, setSQL] = useState<string>(tab.initialSQL ?? defaultSQL);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ExecuteResponse | null>(null);
  const [aiOpen, setAIOpen] = useState(false);
  const [aiLoading, setAILoading] = useState(false);
  const [aiPrompt, setAIPrompt] = useState("");
  const [aiSearch, setAISearch] = useState("");
  const [aiResp, setAIResp] = useState<{ sql: string; explanation?: string } | null>(null);

  const [saveOpen, setSaveOpen] = useState(false);
  const [saveLoading, setSaveLoading] = useState(false);
  const [saveForm] = Form.useForm();

  const SQL_RATIO_KEY = "chat2db.sql.ratio";
  const splitRef = useRef<HTMLDivElement | null>(null);
  const [editorRatio, setEditorRatio] = useState<number>(() => {
    const saved = Number(localStorage.getItem(SQL_RATIO_KEY));
    return Number.isFinite(saved) && saved >= 0.15 && saved <= 0.9 ? saved : 0.6;
  });
  const [vResizing, setVResizing] = useState(false);
  const vResizingRef = useRef(false);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!vResizingRef.current || !splitRef.current) return;
      const rect = splitRef.current.getBoundingClientRect();
      const ratio = (e.clientY - rect.top) / rect.height;
      const clamped = Math.min(0.9, Math.max(0.15, ratio));
      setEditorRatio(clamped);
    };
    const onUp = () => {
      if (!vResizingRef.current) return;
      vResizingRef.current = false;
      setVResizing(false);
      document.body.classList.remove("is-resizing-v");
      setEditorRatio((r) => {
        localStorage.setItem(SQL_RATIO_KEY, String(r));
        return r;
      });
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  const startVResize = (e: React.MouseEvent) => {
    e.preventDefault();
    vResizingRef.current = true;
    setVResizing(true);
    document.body.classList.add("is-resizing-v");
  };

  const [groupSaved, setGroupSaved] = useState<SavedQuery[] | null>(null);
  const [groupOpen, setGroupOpen] = useState(false);

  // @ 引用表的补全索引：预先拉取当前连接里所有 schema 下的所有表
  const [tableIndex, setTableIndex] = useState<Array<{ schema: string; table: string; kind: string }>>([]);
  const [indexLoading, setIndexLoading] = useState(false);

  const loadTableIndex = async () => {
    setIndexLoading(true);
    try {
      const schemas = await api.listSchemas(tab.connID, tab.database);
      const all = await Promise.all(
        schemas.map(async (s) => {
          try {
            const ts = await api.listTables(tab.connID, s.name, tab.database);
            return ts.map((t) => ({ schema: s.name, table: t.name, kind: t.kind }));
          } catch {
            return [];
          }
        })
      );
      setTableIndex(all.flat());
    } catch (e: any) {
      // 静默失败：@ 补全只是辅助功能
    } finally {
      setIndexLoading(false);
    }
  };

  // 判断表名是否在多个 schema 下存在，用于决定插入 "table" 还是 "schema.table"
  const dupNameSet = useMemo(() => {
    const cnt: Record<string, number> = {};
    for (const t of tableIndex) cnt[t.table] = (cnt[t.table] ?? 0) + 1;
    return new Set(Object.keys(cnt).filter((k) => cnt[k] > 1));
  }, [tableIndex]);

  // 根据 Mentions 的当前搜索词返回过滤后的补全选项（最多 100 条，兼顾性能与完整性）
  const filteredMentionOptions = useMemo(() => {
    const q = aiSearch.trim().toLowerCase();
    let candidates = tableIndex;
    if (q) {
      // 同时支持匹配 "table"、"schema.table" 与 schema 前缀
      const scored = tableIndex
        .map((t) => {
          const full = `${t.schema}.${t.table}`.toLowerCase();
          const name = t.table.toLowerCase();
          let score = -1;
          if (name === q || full === q) score = 100;
          else if (name.startsWith(q)) score = 80;
          else if (full.startsWith(q)) score = 70;
          else if (name.includes(q)) score = 40;
          else if (full.includes(q)) score = 30;
          return { t, score };
        })
        .filter((x) => x.score >= 0)
        .sort((a, b) => b.score - a.score)
        .map((x) => x.t);
      candidates = scored;
    }
    return candidates.slice(0, 100).map((t) => {
      const value = dupNameSet.has(t.table) ? `${t.schema}.${t.table}` : t.table;
      return {
        key: `${t.schema}.${t.table}`,
        value,
        label: (
          <Space size={6}>
            <span>{t.table}</span>
            <Typography.Text type="secondary" style={{ fontSize: 11 }}>
              {t.schema} · {t.kind}
            </Typography.Text>
          </Space>
        ),
      };
    });
  }, [tableIndex, aiSearch, dupNameSet]);

  useEffect(() => {
    if (tab.initialSQL && editorRef.current) {
      editorRef.current.setValue(tab.initialSQL);
      setSQL(tab.initialSQL);
    }
  }, [tab.initialSQL]);

  const onMount: OnMount = (ed, monaco) => {
    editorRef.current = ed;
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => runSQL());
    ed.addCommand(monaco.KeyMod.Alt | monaco.KeyCode.KeyI, () => openAI());
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => openSave());
  };

  const runSQL = async () => {
    const ed = editorRef.current;
    let toRun = sql;
    if (ed) {
      const sel = ed.getModel()?.getValueInRange(ed.getSelection()!);
      if (sel && sel.trim()) toRun = sel;
    }
    if (!toRun.trim()) {
      message.warning("没有可执行的 SQL");
      return;
    }
    setRunning(true);
    setResult(null);
    try {
      const res = await api.execute(tab.connID, toRun, tab.database);
      setResult(res);
      if (res.error) {
        message.error("执行报错：" + res.error);
      } else {
        message.success(`执行成功，共 ${res.results.length} 条结果`);
      }
    } catch (e: any) {
      const text = e?.response?.data?.error ?? e?.message ?? "执行失败";
      setResult({ results: [], error: text });
      message.error(text);
    } finally {
      setRunning(false);
    }
  };

  const openAI = () => {
    setAIPrompt("");
    setAIResp(null);
    setAIOpen(true);
    // 懒加载 @ 补全索引
    if (tableIndex.length === 0 && !indexLoading) {
      loadTableIndex();
    }
  };

  // 解析 prompt 里的 @schema.table 或 @table，把对应 DDL 组装成上下文
  const resolveMentions = async (prompt: string): Promise<string> => {
    const re = /@([a-zA-Z_][a-zA-Z0-9_]*)(?:\.([a-zA-Z_][a-zA-Z0-9_]*))?/g;
    const seen = new Set<string>();
    const tasks: Array<Promise<string>> = [];
    let m;
    while ((m = re.exec(prompt)) !== null) {
      const [, a, b] = m;
      let schema: string | undefined;
      let table: string;
      if (b) {
        schema = a;
        table = b;
      } else {
        table = a;
      }
      // 若未指定 schema，尝试从索引中唯一匹配
      if (!schema) {
        const hits = tableIndex.filter((t) => t.table === table);
        if (hits.length === 1) schema = hits[0].schema;
        else if (hits.length > 1) {
          // 多个同名表，优先 public
          const pub = hits.find((x) => x.schema === "public");
          schema = (pub ?? hits[0]).schema;
        } else {
          schema = "public";
        }
      }
      const key = `${schema}.${table}`;
      if (seen.has(key)) continue;
      seen.add(key);
      tasks.push(
        api
          .getTableDDL(tab.connID, schema, table, tab.database)
          .then((r) => r.ddl)
          .catch(() => `-- 获取 ${key} 的 DDL 失败`)
      );
    }
    if (tasks.length === 0) return "";
    const ddls = await Promise.all(tasks);
    return ddls.filter(Boolean).join("\n\n");
  };

  const askAI = async () => {
    if (!aiPrompt.trim()) return;
    setAILoading(true);
    try {
      const ed = editorRef.current;
      const selection = ed?.getModel()?.getValueInRange(ed.getSelection()!) ?? "";
      const tableDDL = await resolveMentions(aiPrompt);
      const resp = await api.aiChat({
        prompt: aiPrompt,
        dialect: tab.driver === "mysql" ? "mysql" : "postgres",
        selection,
        table_ddl: tableDDL || undefined,
      });
      setAIResp({ sql: resp.sql, explanation: resp.explanation });
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "AI 调用失败");
    } finally {
      setAILoading(false);
    }
  };

  const insertAIResult = () => {
    const ed = editorRef.current;
    if (!ed || !aiResp?.sql) return;
    const sel = ed.getSelection();
    ed.executeEdits("ai-insert", [
      {
        range: sel!,
        text: aiResp.sql,
        forceMoveMarkers: true,
      },
    ]);
    setSQL(ed.getValue());
    setAIOpen(false);
  };

  const openSave = () => {
    saveForm.resetFields();
    setSaveOpen(true);
  };

  const submitSave = async () => {
    const v = await saveForm.validateFields();
    setSaveLoading(true);
    try {
      const ed = editorRef.current;
      const sel = ed?.getModel()?.getValueInRange(ed.getSelection()!);
      const sqlToSave = sel && sel.trim() ? sel : sql;
      await api.createSavedQuery({
        connection_id: tab.connID,
        title: v.title,
        description: v.description ?? "",
        sql: sqlToSave,
      });
      message.success("已收藏到当前组");
      setSaveOpen(false);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "收藏失败");
    } finally {
      setSaveLoading(false);
    }
  };

  const openGroupSaved = async () => {
    setGroupOpen(true);
    try {
      // We need the connection's group ID — fetch via a lightweight scheme:
      // group_id is embedded inside SavedQuery view, but to list all saved
      // queries we list "/me/saved-queries" and filter to this connection.
      const all = await api.listMySavedQueries();
      const cur = all.filter((q) => q.connection_id === tab.connID);
      setGroupSaved(cur);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载失败");
    }
  };

  return (
    <div className="sql-tab-root">
      <div className="sql-toolbar">
        <Space>
          <Button
            type="primary"
            size="small"
            icon={<CaretRightOutlined />}
            onClick={runSQL}
            loading={running}
          >
            执行
          </Button>
          <Button size="small" icon={<RobotOutlined />} onClick={openAI}>
            AI
          </Button>
          <Button size="small" icon={<StarOutlined />} onClick={openSave}>
            收藏
          </Button>
          <Button size="small" icon={<TeamOutlined />} onClick={openGroupSaved}>
            组内收藏
          </Button>
        </Space>
        <Space size={4} style={{ marginLeft: "auto" }}>
          {tab.database && (
            <Tag color="blue">
              DB: {tab.database}
            </Tag>
          )}
          <Tag color={tab.role === "owner" ? "orange" : tab.role === "editor" ? "green" : "default"}>
            权限：{tab.role}
          </Tag>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            <span className="kbd">⌘ ↵</span> 执行 · <span className="kbd">⌥ I</span> AI ·{" "}
            <span className="kbd">⌘ S</span> 收藏
          </Typography.Text>
        </Space>
      </div>
      <div className="sql-split" ref={splitRef}>
        <div className="sql-editor-pane" style={{ height: `${editorRatio * 100}%` }}>
          <Editor
            language="sql"
            theme="vs"
            value={sql}
            onChange={(v) => setSQL(v ?? "")}
            onMount={onMount}
            options={{
              fontSize: 13,
              minimap: { enabled: false },
              automaticLayout: true,
              scrollBeyondLastLine: false,
              wordWrap: "on",
            }}
          />
        </div>
        <div
          className={`sql-split-resizer${vResizing ? " resizing" : ""}`}
          onMouseDown={startVResize}
          onDoubleClick={() => setEditorRatio(0.6)}
          title="拖拽调整高度，双击恢复默认"
        />
        <div className="sql-results-pane" style={{ height: `${(1 - editorRatio) * 100}%` }}>
          <ResultsPane result={result} />
        </div>
      </div>

      <Modal
        title="AI 协助写 SQL"
        open={aiOpen}
        width={720}
        onCancel={() => setAIOpen(false)}
        footer={
          aiResp ? (
            <Space>
              <Button onClick={() => setAIResp(null)}>重新提问</Button>
              <Button type="primary" onClick={insertAIResult}>
                插入到编辑器
              </Button>
            </Space>
          ) : (
            <Space>
              <Button onClick={() => setAIOpen(false)}>取消</Button>
              <Button type="primary" onClick={askAI} loading={aiLoading}>
                询问
              </Button>
            </Space>
          )
        }
      >
        {!aiResp ? (
          <>
            <Typography.Paragraph type="secondary">
              输入 <span className="kbd">@</span> 可引用当前连接里的表，提交时会自动把该表的 DDL 一并发给模型。你选中的 SQL 片段也会作为上下文发送。当前方言：{tab.driver === "mysql" ? "mysql" : "postgres"}。
              {indexLoading && <span style={{ marginLeft: 8, color: "#1677ff" }}>表索引加载中…</span>}
            </Typography.Paragraph>
            <Mentions
              autoFocus
              rows={6}
              value={aiPrompt}
              onChange={(v) => setAIPrompt(v)}
              onSearch={(text) => setAISearch(text)}
              onSelect={() => setAISearch("")}
              placeholder="例如：请按 created_at 倒序查询 @users 里 status=1 的前 50 行"
              prefix="@"
              split=" "
              filterOption={false}
              className="ai-mentions"
              options={filteredMentionOptions}
              notFoundContent={
                indexLoading
                  ? "加载中…"
                  : tableIndex.length === 0
                  ? "尚未加载表索引"
                  : "无匹配表"
              }
            />
            <MentionedChips
              prompt={aiPrompt}
              tableIndex={tableIndex}
              onRemove={(raw) => {
                const re = new RegExp(`\\s?@${raw.replace(/[.]/g, "\\.")}\\b`);
                setAIPrompt((p) => p.replace(re, ""));
              }}
            />
            {tableIndex.length === 0 && !indexLoading && (
              <div style={{ marginTop: 8 }}>
                <Button size="small" onClick={loadTableIndex}>
                  加载 @ 表补全
                </Button>
              </div>
            )}
          </>
        ) : (
          <>
            <Typography.Paragraph strong>建议 SQL：</Typography.Paragraph>
            <pre
              style={{
                background: "#0f172a",
                color: "#e2e8f0",
                padding: 12,
                borderRadius: 4,
                maxHeight: 320,
                overflow: "auto",
              }}
            >
              {aiResp.sql}
            </pre>
            {aiResp.explanation && (
              <Typography.Paragraph type="secondary" style={{ marginTop: 8 }}>
                {aiResp.explanation}
              </Typography.Paragraph>
            )}
          </>
        )}
      </Modal>

      <Modal
        title="收藏为组共享 SQL"
        open={saveOpen}
        onCancel={() => setSaveOpen(false)}
        onOk={submitSave}
        confirmLoading={saveLoading}
        okText="保存"
      >
        <Typography.Paragraph type="secondary">
          保存后，当前组内所有成员可见和引用。
          {editorRef.current?.getSelection() &&
          editorRef.current.getModel()?.getValueInRange(editorRef.current.getSelection()!).trim()
            ? "（将保存你当前选中的 SQL 片段）"
            : "（将保存整个编辑器中的 SQL）"}
        </Typography.Paragraph>
        <Form form={saveForm} layout="vertical">
          <Form.Item name="title" label="标题" rules={[{ required: true }]}>
            <Input placeholder="必填" />
          </Form.Item>
          <Form.Item name="description" label="描述（可选）">
            <Input.TextArea rows={3} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="组内收藏（当前连接）"
        open={groupOpen}
        onCancel={() => setGroupOpen(false)}
        footer={null}
        width={760}
      >
        {!groupSaved || groupSaved.length === 0 ? (
          <Empty description="该连接暂无组内收藏" />
        ) : (
          <Tabs
            items={groupSaved.map((q) => ({
              key: String(q.id),
              label: q.title,
              children: (
                <div>
                  {q.description && (
                    <Typography.Paragraph type="secondary">{q.description}</Typography.Paragraph>
                  )}
                  <pre
                    style={{
                      background: "#f5f6f8",
                      padding: 12,
                      borderRadius: 4,
                      maxHeight: 320,
                      overflow: "auto",
                    }}
                  >
                    {q.sql}
                  </pre>
                  <Space>
                    <Button
                      type="primary"
                      onClick={() => {
                        editorRef.current?.setValue(q.sql);
                        setSQL(q.sql);
                        setGroupOpen(false);
                      }}
                    >
                      替换编辑器内容
                    </Button>
                    <Button
                      onClick={() => {
                        const ed = editorRef.current;
                        if (!ed) return;
                        const sel = ed.getSelection();
                        ed.executeEdits("insert-saved", [
                          { range: sel!, text: q.sql, forceMoveMarkers: true },
                        ]);
                        setSQL(ed.getValue());
                        setGroupOpen(false);
                      }}
                    >
                      插入到光标位置
                    </Button>
                  </Space>
                </div>
              ),
            }))}
          />
        )}
      </Modal>
    </div>
  );
}

function ResultsPane({ result }: { result: ExecuteResponse | null }) {
  if (!result) {
    return (
      <div style={{ padding: 16, color: "#9ca3af" }}>
        执行后的结果会显示在此处。
      </div>
    );
  }
  if (result.error) {
    return (
      <Result
        status="error"
        title="SQL 执行失败"
        subTitle={result.error}
        style={{ padding: 16 }}
      />
    );
  }
  return (
    <Tabs
      style={{ padding: "0 12px" }}
      items={result.results.map((r, idx) => ({
        key: String(idx),
        label: `结果 ${idx + 1} (${r.rows?.length ?? 0} 行, ${r.elapsed_ms}ms)`,
        children: <SingleResult result={r} />,
      }))}
    />
  );
}

function SingleResult({ result }: { result: QueryResult }) {
  if (!result.columns || result.columns.length === 0) {
    return (
      <div style={{ padding: 12 }}>
        <Tag color="green">{result.tag || "OK"}</Tag>
        <span style={{ marginLeft: 8 }}>{result.rows_affected} 行受影响</span>
      </div>
    );
  }
  const columns = result.columns.map((name, i) => ({
    title: (
      <Space>
        <span>{name}</span>
        {result.types?.[i] && (
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            {result.types[i]}
          </Typography.Text>
        )}
      </Space>
    ),
    dataIndex: i,
    key: name + "_" + i,
    ellipsis: true,
    render: (v: any) => formatCell(v),
  }));
  const data = (result.rows ?? []).map((r, i) => ({ key: i, ...r }));
  return (
    <div>
      <Space style={{ marginBottom: 8 }}>
        {result.truncated && <Tag color="orange">结果已截断到上限</Tag>}
        <Tooltip title="复制为 TSV">
          <Button
            size="small"
            icon={<CopyOutlined />}
            onClick={() => {
              const lines = [result.columns!.join("\t")];
              (result.rows ?? []).forEach((r) =>
                lines.push(r.map((c) => formatCell(c)).join("\t"))
              );
              navigator.clipboard.writeText(lines.join("\n"));
            }}
          >
            复制
          </Button>
        </Tooltip>
        <Tooltip title="下载 CSV">
          <Button
            size="small"
            icon={<DownloadOutlined />}
            onClick={() => downloadCSV(result)}
          >
            CSV
          </Button>
        </Tooltip>
      </Space>
      <Table
        size="small"
        bordered
        columns={columns}
        dataSource={data}
        pagination={{ pageSize: 50, showSizeChanger: true }}
        scroll={{ x: "max-content", y: 280 }}
      />
    </div>
  );
}

function formatCell(v: any): string {
  if (v === null || v === undefined) return "∅";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function downloadCSV(r: QueryResult) {
  const lines = [r.columns!.map(csvEscape).join(",")];
  (r.rows ?? []).forEach((row) =>
    lines.push(row.map((c) => csvEscape(formatCell(c))).join(","))
  );
  const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "result.csv";
  a.click();
  URL.revokeObjectURL(url);
}

function csvEscape(s: string): string {
  if (s.includes(",") || s.includes("\n") || s.includes('"')) {
    return '"' + s.replace(/"/g, '""') + '"';
  }
  return s;
}

// MentionedChips：从 prompt 中解析出 @xxx 引用，以 Tag 的形式展示，并可关闭
function MentionedChips({
  prompt,
  tableIndex,
  onRemove,
}: {
  prompt: string;
  tableIndex: Array<{ schema: string; table: string; kind: string }>;
  onRemove: (raw: string) => void;
}) {
  const items = useMemo(() => {
    const re = /@([a-zA-Z_][a-zA-Z0-9_]*)(?:\.([a-zA-Z_][a-zA-Z0-9_]*))?/g;
    const out: Array<{ raw: string; display: string; resolved: boolean; schema?: string; table: string }> = [];
    const seen = new Set<string>();
    let m;
    while ((m = re.exec(prompt)) !== null) {
      const [full, a, b] = m;
      const raw = full.slice(1);
      if (seen.has(raw)) continue;
      seen.add(raw);
      let schema: string | undefined;
      let table: string;
      if (b) {
        schema = a;
        table = b;
      } else {
        table = a;
        const hits = tableIndex.filter((t) => t.table === table);
        if (hits.length === 1) schema = hits[0].schema;
        else if (hits.length > 1)
          schema = (hits.find((x) => x.schema === "public") ?? hits[0]).schema;
      }
      const resolved = tableIndex.some(
        (t) => t.table === table && (schema ? t.schema === schema : true)
      );
      out.push({ raw, display: schema ? `${schema}.${table}` : table, resolved, schema, table });
    }
    return out;
  }, [prompt, tableIndex]);

  if (items.length === 0) return null;
  return (
    <div style={{ marginTop: 8, display: "flex", gap: 6, flexWrap: "wrap" }}>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        已引用：
      </Typography.Text>
      {items.map((it) => (
        <Tag
          key={it.raw}
          color={it.resolved ? "blue" : "red"}
          closable
          onClose={(e) => {
            e.preventDefault();
            onRemove(it.raw);
          }}
          style={{ margin: 0 }}
        >
          @{it.display}
          {!it.resolved && "（未找到）"}
        </Tag>
      ))}
    </div>
  );
}
