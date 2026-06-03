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
  Upload,
  Progress,
} from "antd";
import {
  CaretRightOutlined,
  StopOutlined,
  RobotOutlined,
  StarOutlined,
  CopyOutlined,
  DownloadOutlined,
  TeamOutlined,
  PaperClipOutlined,
  DeleteOutlined,
  FileImageOutlined,
  FileExcelOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import Editor, { OnMount } from "@monaco-editor/react";
import type { editor as MonacoEditor } from "monaco-editor";
import { api } from "../api";
import type { ExecuteResponse, QueryResult, SavedQuery } from "../types";
import type { OpenedTab } from "../pages/MainLayout";
import { ROLE_TAG_COLOR } from "../utils/role";

interface Props {
  tab: OpenedTab;
}

export default function SQLTab({ tab }: Props) {
  const { message } = App.useApp();
  const editorRef = useRef<MonacoEditor.IStandaloneCodeEditor | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const defaultSQL = "";
  const [sql, setSQL] = useState<string>(tab.initialSQL ?? defaultSQL);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ExecuteResponse | null>(null);
  const [aiOpen, setAIOpen] = useState(false);
  const [aiLoading, setAILoading] = useState(false);
  const [aiPrompt, setAIPrompt] = useState("");
  const [aiSearch, setAISearch] = useState("");
  const [aiResp, setAIResp] = useState<{ sql: string; explanation?: string } | null>(null);
  // 打开 AI 弹窗时快照编辑器当前选区（行号 + 文本），失焦后也能稳定展示
  const [aiSelection, setAISelection] = useState<{
    startLine: number;
    endLine: number;
    text: string;
  } | null>(null);
  const [aiSelExpanded, setAISelExpanded] = useState(false);

  // 文件上传相关状态
  interface UploadedFile {
    file_id: string;
    filename: string;
    size: number;
    file_type: string;
    uploaded_at: number;
    data_summary?: string; // 数据摘要
  }
  const [uploadedFiles, setUploadedFiles] = useState<UploadedFile[]>([]);
  const [uploading, setUploading] = useState(false);

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

  // 组件卸载时中止进行中的请求，防止内存泄漏
  useEffect(() => {
    return () => { abortRef.current?.abort(); };
  }, []);

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
    const controller = new AbortController();
    abortRef.current = controller;
    setRunning(true);
    setResult(null);
    try {
      const res = await api.execute(tab.connID, toRun, tab.database, controller.signal);
      setResult(res);
      if (res.error) {
        message.error("执行报错：" + res.error);
      } else {
        message.success(`执行成功，共 ${res.results.length} 条结果`);
      }
    } catch (e: any) {
      if (e.code === "ERR_CANCELED" || e.name === "CanceledError") {
        message.info("查询已取消");
      } else {
        const text = e?.response?.data?.error ?? e?.message ?? "执行失败";
        setResult({ results: [], error: text });
        message.error(text);
      }
    } finally {
      abortRef.current = null;
      setRunning(false);
    }
  };

  const cancelSQL = () => {
    abortRef.current?.abort();
  };

  const openAI = () => {
    setAIPrompt("");
    setAIResp(null);
    setUploadedFiles([]); // 清空之前上传的文件
    // 快照当前编辑器选区
    const ed = editorRef.current;
    const sel = ed?.getSelection();
    const model = ed?.getModel();
    if (ed && sel && model) {
      const text = model.getValueInRange(sel);
      if (text && text.trim()) {
        setAISelection({
          startLine: sel.startLineNumber,
          endLine: sel.endLineNumber,
          text,
        });
      } else {
        setAISelection(null);
      }
    } else {
      setAISelection(null);
    }
    setAISelExpanded(false);
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

  // 从 SQL 中自动提取表名并获取 DDL
  const extractTablesFromSQL = async (sql: string): Promise<string> => {
    if (!sql.trim()) return "";

    // 简单的 SQL 解析：提取 FROM 和 JOIN 后面的表名
    // 支持格式：schema.table, "schema"."table", `schema`.`table`, table
    const tablePattern = /(?:FROM|JOIN)\s+(?:([a-zA-Z_][a-zA-Z0-9_]*)\s*\.\s*)?([a-zA-Z_][a-zA-Z0-9_]*)|(?:FROM|JOIN)\s+(?:"([^"]+)"\s*\.\s*"([^"]+)"|`([^`]+)`\s*\.\s*`([^`]+)`)/gi;

    const seen = new Set<string>();
    const tasks: Array<Promise<string>> = [];
    let match;

    while ((match = tablePattern.exec(sql)) !== null) {
      let schema: string | undefined;
      let table: string;

      // 处理不同的匹配组
      if (match[1] && match[2]) {
        // schema.table
        schema = match[1];
        table = match[2];
      } else if (match[3] && match[4]) {
        // "schema"."table"
        schema = match[3];
        table = match[4];
      } else if (match[5] && match[6]) {
        // `schema`.`table`
        schema = match[5];
        table = match[6];
      } else if (match[2]) {
        // table only
        table = match[2];
      } else {
        continue;
      }

      // 若未指定 schema，尝试从索引中匹配
      if (!schema) {
        const hits = tableIndex.filter((t) => t.table === table);
        if (hits.length === 1) {
          schema = hits[0].schema;
        } else if (hits.length > 1) {
          const pub = hits.find((x) => x.schema === "public");
          schema = (pub ?? hits[0]).schema;
        } else {
          schema = tab.schema || "public";
        }
      }

      const key = `${schema}.${table}`;
      if (seen.has(key)) continue;
      seen.add(key);

      tasks.push(
        api
          .getTableDDL(tab.connID, schema, table, tab.database)
          .then((r) => `-- Table: ${key}\n${r.ddl}`)
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
      // 优先使用打开弹窗时快照到的选区，而不是再次读取 Monaco（避免 modal 内失焦后 selection 为空）
      const selection = aiSelection?.text ?? "";

      // 1. 解析 prompt 中的 @ 引用
      const mentionDDL = await resolveMentions(aiPrompt);

      // 2. 如果有选中的 SQL，自动提取其中的表名并获取 DDL
      const sqlDDL = selection ? await extractTablesFromSQL(selection) : "";

      // 3. 合并两部分 DDL
      const combinedDDL = [mentionDDL, sqlDDL].filter(Boolean).join("\n\n");

      // 4. 处理上传的文件
      let fileContext = "";
      if (uploadedFiles.length > 0) {
        fileContext = "\n\n用户上传了以下文件：\n";

        for (const file of uploadedFiles) {
          fileContext += `\n文件名: ${file.filename}\n`;

          // 如果有数据摘要，直接包含进来
          if (file.data_summary) {
            fileContext += file.data_summary + "\n";
          } else {
            fileContext += `文件类型: ${file.file_type}\n`;
          }
        }

        fileContext += "\n请根据上述文件数据回答用户的问题。";
      }

      // 根据驱动类型设置方言
      let dialect = "postgres";
      if (tab.driver === "mysql") {
        dialect = "mysql";
      } else if (tab.driver === "hive") {
        dialect = "hive";
      }
      const resp = await api.aiChat({
        prompt: aiPrompt + fileContext,
        dialect,
        selection,
        table_ddl: combinedDDL || undefined,
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

  // 文件上传处理
  const handleFileUpload = async (file: File) => {
    setUploading(true);
    try {
      const formData = new FormData();
      formData.append("file", file);

      const resp = await api.uploadFile(formData);
      setUploadedFiles((prev) => [...prev, resp]);
      message.success(`文件 ${file.name} 上传成功`);
      return false; // 阻止默认上传行为
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "文件上传失败");
      return false;
    } finally {
      setUploading(false);
    }
  };

  // 删除已上传的文件
  const handleFileRemove = async (fileId: string) => {
    try {
      await api.deleteUploadedFile(fileId);
      setUploadedFiles((prev) => prev.filter((f) => f.file_id !== fileId));
      message.success("文件已删除");
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "删除失败");
    }
  };

  // 获取文件图标
  const getFileIcon = (fileType: string) => {
    if ([".jpg", ".jpeg", ".png", ".gif", ".bmp"].includes(fileType)) {
      return <FileImageOutlined style={{ fontSize: 16, color: "#52c41a" }} />;
    }
    if ([".xlsx", ".xls"].includes(fileType)) {
      return <FileExcelOutlined style={{ fontSize: 16, color: "#1890ff" }} />;
    }
    if ([".csv", ".txt"].includes(fileType)) {
      return <FileTextOutlined style={{ fontSize: 16, color: "#faad14" }} />;
    }
    return <FileTextOutlined style={{ fontSize: 16 }} />;
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
          {running ? (
            <Button
              danger
              size="small"
              icon={<StopOutlined />}
              onClick={cancelSQL}
            >
              取消
            </Button>
          ) : (
            <Button
              type="primary"
              size="small"
              icon={<CaretRightOutlined />}
              onClick={runSQL}
            >
              执行
            </Button>
          )}
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
          <Tag color={ROLE_TAG_COLOR[tab.role]}>
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
              输入 <span className="kbd">@</span> 可引用当前连接里的表，提交时会自动把该表的 DDL 一并发给模型。当前方言：{tab.driver === "hive" ? "hive" : tab.driver === "mysql" ? "mysql" : "postgres"}。
              {indexLoading && <span style={{ marginLeft: 8, color: "#1677ff" }}>表索引加载中…</span>}
            </Typography.Paragraph>
            <SelectionPreview
              selection={aiSelection}
              expanded={aiSelExpanded}
              onToggle={() => setAISelExpanded((v) => !v)}
              onRemove={() => setAISelection(null)}
              onLocate={() => {
                const ed = editorRef.current;
                if (!ed || !aiSelection) return;
                ed.revealLinesInCenter(aiSelection.startLine, aiSelection.endLine);
                ed.setSelection({
                  startLineNumber: aiSelection.startLine,
                  startColumn: 1,
                  endLineNumber: aiSelection.endLine,
                  endColumn: Number.MAX_SAFE_INTEGER,
                });
              }}
            />

            {/* 文件上传区域 */}
            {uploadedFiles.length > 0 && (
              <div
                style={{
                  marginBottom: 12,
                  border: "1px solid #d9e2ec",
                  background: "#f5f8ff",
                  borderRadius: 6,
                  padding: "8px 10px",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 8 }}>
                  <Tag color="cyan" style={{ margin: 0 }}>
                    已上传文件
                  </Tag>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    （{uploadedFiles.length} 个文件）
                  </Typography.Text>
                </div>
                <Space direction="vertical" style={{ width: "100%" }} size={4}>
                  {uploadedFiles.map((file) => (
                    <div
                      key={file.file_id}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 8,
                        padding: "4px 8px",
                        background: "#fff",
                        borderRadius: 4,
                        border: "1px solid #e8e8e8",
                      }}
                    >
                      {getFileIcon(file.file_type)}
                      <Typography.Text style={{ flex: 1, fontSize: 12 }}>
                        {file.filename}
                      </Typography.Text>
                      <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                        {(file.size / 1024).toFixed(1)} KB
                      </Typography.Text>
                      <Button
                        type="text"
                        size="small"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() => handleFileRemove(file.file_id)}
                      />
                    </div>
                  ))}
                </Space>
              </div>
            )}

            <Upload
              beforeUpload={handleFileUpload}
              showUploadList={false}
              accept=".jpg,.jpeg,.png,.gif,.bmp,.xlsx,.xls,.csv,.txt"
              disabled={uploading}
            >
              <Button
                icon={<PaperClipOutlined />}
                loading={uploading}
                size="small"
                style={{ marginBottom: 12 }}
              >
                上传文件（图片/Excel/CSV）
              </Button>
            </Upload>

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

// SelectionPreview：在 AI 弹窗里展示"引用了编辑器哪几行的 SQL 片段"，
// 含行号范围、折叠/展开预览、定位到编辑器、移除引用
function SelectionPreview({
  selection,
  expanded,
  onToggle,
  onRemove,
  onLocate,
}: {
  selection: { startLine: number; endLine: number; text: string } | null;
  expanded: boolean;
  onToggle: () => void;
  onRemove: () => void;
  onLocate: () => void;
}) {
  if (!selection) return null;
  const { startLine, endLine, text } = selection;
  const lineCount = endLine - startLine + 1;
  const charCount = text.length;
  const rangeLabel = startLine === endLine ? `第 ${startLine} 行` : `第 ${startLine} – ${endLine} 行`;
  // 折叠时只展示首行摘要（截断 120 字），展开时用 pre 显示完整片段
  const firstLine = text.split(/\r?\n/)[0] ?? "";
  const summary = firstLine.length > 120 ? firstLine.slice(0, 120) + "…" : firstLine;

  return (
    <div
      style={{
        marginBottom: 12,
        border: "1px solid #d9e2ec",
        background: "#f5f8ff",
        borderRadius: 6,
        padding: "8px 10px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        <Tag color="geekblue" style={{ margin: 0 }}>
          编辑器引用
        </Tag>
        <Typography.Text strong style={{ fontSize: 12 }}>
          {rangeLabel}
        </Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          （{lineCount} 行 · {charCount} 字符）
        </Typography.Text>
        <span style={{ marginLeft: "auto" }}>
          <Button size="small" type="link" onClick={onToggle} style={{ padding: "0 4px" }}>
            {expanded ? "收起" : "展开预览"}
          </Button>
          <Button size="small" type="link" onClick={onLocate} style={{ padding: "0 4px" }}>
            定位到编辑器
          </Button>
          <Button
            size="small"
            type="link"
            danger
            onClick={onRemove}
            style={{ padding: "0 4px" }}
          >
            移除
          </Button>
        </span>
      </div>
      {expanded ? (
        <pre
          style={{
            marginTop: 8,
            marginBottom: 0,
            background: "#0f172a",
            color: "#e2e8f0",
            padding: 10,
            borderRadius: 4,
            maxHeight: 180,
            overflow: "auto",
            fontSize: 12,
            lineHeight: 1.5,
            whiteSpace: "pre",
          }}
        >
          {text}
        </pre>
      ) : (
        <Typography.Text
          type="secondary"
          style={{
            display: "block",
            marginTop: 6,
            fontSize: 12,
            fontFamily:
              "SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {summary || <i>（空白行）</i>}
        </Typography.Text>
      )}
    </div>
  );
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
