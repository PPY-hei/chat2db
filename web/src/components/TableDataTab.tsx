import { useEffect, useMemo, useRef, useState } from "react";
import {
  App,
  Button,
  Input,
  Modal,
  Popover,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  ReloadOutlined,
  FileSearchOutlined,
  CodeOutlined,
  CopyOutlined,
  FilterOutlined,
  PlusOutlined,
  CloseOutlined,
  SortAscendingOutlined,
  SortDescendingOutlined,
  SaveOutlined,
  UndoOutlined,
  EyeOutlined,
} from "@ant-design/icons";
import { api } from "../api";
import type { ColumnInfo, ExecuteResponse } from "../types";
import type { OpenedTab } from "../pages/MainLayout";

interface Props {
  tab: OpenedTab;
  onOpenSQL: (sql: string) => void;
}

type Op =
  | "="
  | "!="
  | ">"
  | ">="
  | "<"
  | "<="
  | "LIKE"
  | "NOT LIKE"
  | "contains"
  | "IN"
  | "NOT IN"
  | "IS NULL"
  | "IS NOT NULL";

const OPS: Op[] = [
  "=",
  "!=",
  ">",
  ">=",
  "<",
  "<=",
  "LIKE",
  "NOT LIKE",
  "contains",
  "IN",
  "NOT IN",
  "IS NULL",
  "IS NOT NULL",
];

// bool 类型只允许这些操作符
const BOOL_OPS: Op[] = ["=", "!=", "IS NULL", "IS NOT NULL"];

interface FilterCond {
  id: number;
  column: string;
  op: Op;
  value: string;
}

type SortDir = "asc" | "desc" | null;

let nextFilterId = 1;

// 判断列的数据类型是否可视为数值（或时间戳），用于决定字面量是否加引号
function isNumericLike(dt: string | undefined): boolean {
  if (!dt) return false;
  const s = dt.toLowerCase();
  return /(int|serial|bigint|smallint|numeric|decimal|real|double|float|money)/.test(s);
}

function isBoolLike(dt: string | undefined): boolean {
  if (!dt) return false;
  const s = dt.toLowerCase();
  return s.startsWith("bool") || s === "tinyint(1)";
}

// SQL 字符串字面量转义
function sqlQuote(s: string, mysql?: boolean): string {
  if (mysql) {
    // MySQL 默认 backslash 是转义字符，需要同时转义 \ 和 '
    return "'" + s.replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'";
  }
  // PostgreSQL standard_conforming_strings=on，只需双写单引号
  return "'" + s.replace(/'/g, "''") + "'";
}

// 把单个值按列类型生成字面量
function literalFor(value: string, dt: string | undefined, mysql?: boolean): string {
  const v = value.trim();
  if (isBoolLike(dt)) {
    const lv = v.toLowerCase();
    if (lv === "true" || lv === "t" || lv === "1") return "TRUE";
    if (lv === "false" || lv === "f" || lv === "0") return "FALSE";
    return sqlQuote(v, mysql);
  }
  if (isNumericLike(dt) && /^-?\d+(\.\d+)?$/.test(v)) return v;
  return sqlQuote(v, mysql);
}

// 将一个筛选条件转成 SQL 片段
function buildWhereClause(cond: FilterCond, colMeta: ColumnInfo | undefined, quoteFn: (s: string) => string, mysql?: boolean): string | null {
  const col = quoteFn(cond.column);
  const dt = colMeta?.data_type;
  switch (cond.op) {
    case "IS NULL":
      return `${col} IS NULL`;
    case "IS NOT NULL":
      return `${col} IS NOT NULL`;
    case "IN":
    case "NOT IN": {
      const parts = cond.value
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean);
      if (parts.length === 0) return null;
      const list = parts.map((p) => literalFor(p, dt, mysql)).join(", ");
      return `${col} ${cond.op} (${list})`;
    }
    case "LIKE":
    case "NOT LIKE":
      if (!cond.value) return null;
      return `${col} ${cond.op} ${sqlQuote(cond.value, mysql)}`;
    case "contains":
      // 大小写不敏感的包含匹配
      if (!cond.value) return null;
      if (mysql) {
        return `${col} LIKE ${sqlQuote("%" + cond.value + "%", true)}`;
      }
      return `${col}::text ILIKE ${sqlQuote("%" + cond.value + "%")}`;
    default:
      if (cond.value === "") return null;
      return `${col} ${cond.op} ${literalFor(cond.value, dt, mysql)}`;
  }
}

export default function TableDataTab({ tab, onOpenSQL }: Props) {
  const { message } = App.useApp();
  const isMySQL = tab.driver === "mysql";
  // SQL 标识符引用：PG 用双引号，MySQL 用反引号
  const q = (name: string) => (isMySQL ? `\`${name}\`` : `"${name}"`);
  // 完整表名引用
  const fullTable = isMySQL
    ? (tab.database ? `\`${tab.database}\`.\`${tab.table}\`` : `\`${tab.table}\``)
    : `"${tab.schema}"."${tab.table}"`;

  const [columns, setColumns] = useState<ColumnInfo[]>([]);
  const [data, setData] = useState<ExecuteResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [pageSize, setPageSize] = useState(50);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState<number | null>(null);
  const [ddlOpen, setDdlOpen] = useState(false);
  const [ddl, setDdl] = useState<string>("");
  const [ddlLoading, setDdlLoading] = useState(false);

  // 筛选与排序
  const [draftFilters, setDraftFilters] = useState<FilterCond[]>([]); // 未应用的编辑
  const [appliedFilters, setAppliedFilters] = useState<FilterCond[]>([]); // 已经用来拉数据的
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>(null);
  const [filterOpen, setFilterOpen] = useState(false);

  // 工具：生成当前查询 SQL 的 WHERE + ORDER BY 后缀
  const buildSuffix = (
    filtersToUse: FilterCond[],
    withLimit: boolean,
    nextPage = page,
    nextPageSize = pageSize
  ): string => {
    const parts: string[] = [];
    const where: string[] = [];
    for (const f of filtersToUse) {
      const meta = columns.find((c) => c.name === f.column);
      const clause = buildWhereClause(f, meta, q, isMySQL);
      if (clause) where.push(clause);
    }
    if (where.length > 0) parts.push("WHERE " + where.join(" AND "));
    if (sortColumn && sortDir) {
      parts.push(`ORDER BY ${q(sortColumn)} ${sortDir === "asc" ? "ASC" : "DESC"}`);
    } else if (!isMySQL) {
      parts.push("ORDER BY ctid");
    }
    if (withLimit) {
      const offset = (nextPage - 1) * nextPageSize;
      parts.push(`LIMIT ${nextPageSize} OFFSET ${offset}`);
    }
    return parts.join(" ");
  };

  const fetchPage = async (
    nextPage: number,
    nextPageSize: number,
    filtersToUse: FilterCond[] = appliedFilters
  ) => {
    if (!tab.schema || !tab.table) return;
    setLoading(true);
    try {
      const suffix = buildSuffix(filtersToUse, true, nextPage, nextPageSize);
      const sql = `SELECT * FROM ${fullTable} ${suffix}`;
      let res: ExecuteResponse;
      if (isMySQL) {
        res = await api.execute(tab.connID, sql, tab.database);
      } else {
        const fallback = sql.replace(/ORDER BY ctid/, "");
        try {
          res = await api.execute(tab.connID, sql, tab.database);
          if (res.error) throw new Error(res.error);
        } catch {
          res = await api.execute(tab.connID, fallback, tab.database);
        }
      }
      setData(res);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载失败");
    } finally {
      setLoading(false);
    }
  };

  const fetchTotal = async (filtersToUse: FilterCond[] = appliedFilters) => {
    if (!tab.schema || !tab.table) return;
    try {
      const whereParts: string[] = [];
      for (const f of filtersToUse) {
        const meta = columns.find((c) => c.name === f.column);
        const clause = buildWhereClause(f, meta, q, isMySQL);
        if (clause) whereParts.push(clause);
      }
      const whereSQL = whereParts.length ? " WHERE " + whereParts.join(" AND ") : "";
      const cntRes = await api.execute(
        tab.connID,
        `SELECT COUNT(*) FROM ${fullTable}${whereSQL}`,
        tab.database
      );
      const v = cntRes.results?.[0]?.rows?.[0]?.[0];
      if (v !== undefined && v !== null) setTotal(Number(v));
      else setTotal(null);
    } catch {
      setTotal(null);
    }
  };

  const reload = async () => {
    if (!tab.schema || !tab.table) return;
    setLoading(true);
    try {
      const cols = await api.listColumns(tab.connID, tab.schema, tab.table, tab.database);
      setColumns(cols);
      await fetchPage(page, pageSize, appliedFilters);
      await fetchTotal(appliedFilters);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载失败");
    } finally {
      setLoading(false);
    }
  };

  const openDDL = async () => {
    if (!tab.schema || !tab.table) return;
    setDdlOpen(true);
    if (ddl) return;
    setDdlLoading(true);
    try {
      const res = await api.getTableDDL(tab.connID, tab.schema, tab.table, tab.database);
      setDdl(res.ddl);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "DDL 加载失败");
    } finally {
      setDdlLoading(false);
    }
  };

  useEffect(() => {
    setPage(1);
    setAppliedFilters([]);
    setDraftFilters([]);
    setSortColumn(null);
    setSortDir(null);
    setDirtyRows(new Map());
    setEditingCell(null);
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.key]);

  // ===================== 内联编辑 =====================
  const canEdit = tab.role === "owner" || tab.role === "editor";
  const pkCols = useMemo(() => columns.filter((c) => c.is_primary).map((c) => c.name), [columns]);
  const hasPK = pkCols.length > 0;

  // dirtyRows: Map<rowIndex, Map<colName, newValue>>
  const [dirtyRows, setDirtyRows] = useState<Map<number, Map<string, string>>>(new Map());
  const [editingCell, setEditingCell] = useState<{ row: number; col: string } | null>(null);
  const [editingValue, setEditingValue] = useState<string>("");
  const editInputRef = useRef<any>(null);
  const [sqlPreviewOpen, setSqlPreviewOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const dirtyCount = dirtyRows.size;

  const startEdit = (rowIdx: number, colName: string, currentVal: any) => {
    if (!canEdit || !hasPK) return;
    setEditingCell({ row: rowIdx, col: colName });
    setEditingValue(currentVal === null || currentVal === undefined ? "" : String(currentVal));
    // 焦点交给下面的 useEffect 处理，避免 setTimeout 带来的竞态
  };

  // editingCell 变化后，等 DOM 完成 commit，再 focus + select。
  // 用 useEffect 而非 setTimeout 能保证 ref 一定指向最新的 Input，
  // 也不会因为组件卸载留下悬挂的 timer。
  useEffect(() => {
    if (!editingCell) return;
    const input = editInputRef.current;
    if (!input) return;
    // 优先用 antd Input 的 focus({ cursor })，没有时退化为原生 focus + select
    if (typeof input.focus === "function") {
      try {
        input.focus({ cursor: "all" });
      } catch {
        input.focus();
      }
    }
    if (typeof input.select === "function") {
      input.select();
    }
  }, [editingCell]);

  const commitEdit = () => {
    if (!editingCell) return;
    const { row, col } = editingCell;
    const originalRow = result?.rows?.[row];
    const colIdx = result?.columns?.indexOf(col);
    if (!originalRow || colIdx === undefined || colIdx < 0) {
      setEditingCell(null);
      return;
    }
    const origVal = originalRow[colIdx];
    const origStr = origVal === null || origVal === undefined ? "" : String(origVal);
    const newStr = editingValue;

    setDirtyRows((prev) => {
      const next = new Map(prev);
      if (newStr === origStr) {
        const rowDirty = next.get(row);
        if (rowDirty) {
          rowDirty.delete(col);
          if (rowDirty.size === 0) next.delete(row);
        }
      } else {
        if (!next.has(row)) next.set(row, new Map());
        next.get(row)!.set(col, newStr);
      }
      return next;
    });
    setEditingCell(null);
  };

  const cancelEdit = () => {
    setEditingCell(null);
  };

  const discardChanges = () => {
    setDirtyRows(new Map());
    setEditingCell(null);
  };

  // 生成 UPDATE SQL（用 PK 做 WHERE 条件）
  const generateUpdateSQL = (): string[] => {
    if (!result?.columns || !result.rows || pkCols.length === 0) return [];
    const stmts: string[] = [];
    for (const [rowIdx, colChanges] of dirtyRows) {
      const row = result.rows[rowIdx];
      if (!row) continue;
      const setParts: string[] = [];
      for (const [col, val] of colChanges) {
        const meta = columns.find((c) => c.name === col);
        if (val === "") {
          setParts.push(`${q(col)} = NULL`);
        } else {
          setParts.push(`${q(col)} = ${literalFor(val, meta?.data_type, isMySQL)}`);
        }
      }
      const whereParts: string[] = [];
      for (const pk of pkCols) {
        const pkIdx = result.columns.indexOf(pk);
        if (pkIdx < 0) continue;
        const pkVal = row[pkIdx];
        const pkMeta = columns.find((c) => c.name === pk);
        if (pkVal === null || pkVal === undefined) {
          whereParts.push(`${q(pk)} IS NULL`);
        } else {
          whereParts.push(`${q(pk)} = ${literalFor(String(pkVal), pkMeta?.data_type, isMySQL)}`);
        }
      }
      stmts.push(
        `UPDATE ${fullTable} SET ${setParts.join(", ")} WHERE ${whereParts.join(" AND ")};`
      );
    }
    return stmts;
  };

  const saveChanges = async () => {
    const stmts = generateUpdateSQL();
    if (stmts.length === 0) return;
    setSaving(true);
    try {
      const sql = stmts.join("\n");
      const res = await api.execute(tab.connID, sql, tab.database);
      if (res.error) {
        message.error("保存失败：" + res.error);
      } else {
        const affected = res.results?.reduce((sum, r) => sum + (r.rows_affected ?? 0), 0) ?? 0;
        message.success(`已保存 ${affected} 行`);
        setDirtyRows(new Map());
        setEditingCell(null);
        await fetchPage(page, pageSize, appliedFilters);
      }
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "保存失败");
    } finally {
      setSaving(false);
    }
  };

  // 筛选条件操作
  const addFilter = () => {
    const firstCol = columns[0]?.name ?? "";
    setDraftFilters((arr) => [
      ...arr,
      { id: nextFilterId++, column: firstCol, op: "=", value: "" },
    ]);
  };

  const updateFilter = (id: number, patch: Partial<FilterCond>) => {
    setDraftFilters((arr) =>
      arr.map((f) => {
        if (f.id !== id) return f;
        const next = { ...f, ...patch };
        // 切换列时若新列是 bool 而旧的 op 不被允许，强制重置 op + value
        if (patch.column !== undefined) {
          const meta = columns.find((c) => c.name === patch.column);
          if (meta && isBoolLike(meta.data_type) && !BOOL_OPS.includes(next.op)) {
            next.op = "=";
            next.value = "";
          }
        }
        // 切换 op 时若新 op 不需要值，清空 value
        if (patch.op === "IS NULL" || patch.op === "IS NOT NULL") {
          next.value = "";
        }
        return next;
      })
    );
  };

  const removeFilter = (id: number) => {
    setDraftFilters((arr) => arr.filter((f) => f.id !== id));
  };

  const applyFilters = async () => {
    setAppliedFilters(draftFilters);
    setPage(1);
    setFilterOpen(false);
    setLoading(true);
    try {
      await fetchPage(1, pageSize, draftFilters);
      await fetchTotal(draftFilters);
    } finally {
      setLoading(false);
    }
  };

  const clearFilters = async () => {
    setDraftFilters([]);
    setAppliedFilters([]);
    setPage(1);
    setLoading(true);
    try {
      await fetchPage(1, pageSize, []);
      await fetchTotal([]);
    } finally {
      setLoading(false);
    }
  };

  // 列头点击：三态切换 无 -> 升 -> 降 -> 无
  const toggleSort = async (colName: string) => {
    let nextCol: string | null = colName;
    let nextDir: SortDir;
    if (sortColumn !== colName) nextDir = "asc";
    else if (sortDir === "asc") nextDir = "desc";
    else if (sortDir === "desc") {
      nextDir = null;
      nextCol = null;
    } else nextDir = "asc";
    setSortColumn(nextCol);
    setSortDir(nextDir);
    setPage(1);
    setLoading(true);
    try {
      // 使用最新的排序值重新取数据
      const filtersToUse = appliedFilters;
      const parts: string[] = [];
      const whereParts: string[] = [];
      for (const f of filtersToUse) {
        const meta = columns.find((c) => c.name === f.column);
        const clause = buildWhereClause(f, meta, q, isMySQL);
        if (clause) whereParts.push(clause);
      }
      if (whereParts.length > 0) parts.push("WHERE " + whereParts.join(" AND "));
      if (nextCol && nextDir) {
        parts.push(`ORDER BY ${q(nextCol)} ${nextDir === "asc" ? "ASC" : "DESC"}`);
      } else if (!isMySQL) {
        parts.push("ORDER BY ctid");
      }
      parts.push(`LIMIT ${pageSize} OFFSET 0`);
      const sql = `SELECT * FROM ${fullTable} ${parts.join(" ")}`;
      let res: ExecuteResponse;
      if (isMySQL) {
        res = await api.execute(tab.connID, sql, tab.database);
      } else {
        const fallback = sql.replace(/ORDER BY ctid/, "");
        try {
          res = await api.execute(tab.connID, sql, tab.database);
          if (res.error) throw new Error(res.error);
        } catch {
          res = await api.execute(tab.connID, fallback, tab.database);
        }
      }
      setData(res);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "排序失败");
    } finally {
      setLoading(false);
    }
  };

  const result = data?.results?.[0];

  const columnOptions = useMemo(
    () =>
      columns.map((c) => ({
        label: `${c.name} (${c.data_type})`,
        value: c.name,
      })),
    [columns]
  );

  const tableColumns =
    result?.columns?.map((name, i) => {
      const meta = columns.find((c) => c.name === name);
      const isSorted = sortColumn === name;
      const sortIcon =
        isSorted && sortDir === "asc" ? (
          <SortAscendingOutlined style={{ color: "#1677ff" }} />
        ) : isSorted && sortDir === "desc" ? (
          <SortDescendingOutlined style={{ color: "#1677ff" }} />
        ) : (
          <SortAscendingOutlined style={{ color: "#cbd5e1" }} />
        );
      return {
        title: (
          <div
            onClick={() => toggleSort(name)}
            style={{ cursor: "pointer", userSelect: "none", display: "flex", gap: 4, alignItems: "center" }}
            title="点击切换排序：升 / 降 / 无"
          >
            <strong>{name}</strong>
            {meta?.is_primary && (
              <Tag color="orange" style={{ margin: 0 }}>
                PK
              </Tag>
            )}
            <Typography.Text type="secondary" style={{ fontSize: 11 }}>
              {meta?.data_type ?? result?.types?.[i] ?? ""}
            </Typography.Text>
            <span style={{ marginLeft: "auto" }}>{sortIcon}</span>
          </div>
        ),
        dataIndex: i,
        key: name + "_" + i,
        ellipsis: editingCell?.col !== name,
        onCell: (record: any) => ({
          onDoubleClick: () => {
            if (canEdit && hasPK) startEdit(record.key, name, record[i]);
          },
        }),
        render: (v: any, record: any) => {
          const rowIdx = record.key as number;
          const isEditing = editingCell?.row === rowIdx && editingCell?.col === name;
          const isDirty = dirtyRows.get(rowIdx)?.has(name);
          const displayVal = isDirty ? dirtyRows.get(rowIdx)!.get(name)! : v;

          if (isEditing) {
            return (
              <Input
                ref={editInputRef}
                size="small"
                value={editingValue}
                onChange={(e) => setEditingValue(e.target.value)}
                onPressEnter={commitEdit}
                onBlur={commitEdit}
                onKeyDown={(e) => {
                  if (e.key === "Escape") cancelEdit();
                }}
                style={{ margin: -4, width: "calc(100% + 8px)" }}
              />
            );
          }

          const cellStyle: React.CSSProperties = isDirty
            ? { background: "#fef3c7", borderRadius: 2, padding: "0 4px", margin: "-0 -4px" }
            : {};

          if (displayVal === null || displayVal === undefined || displayVal === "") {
            return (
              <span style={{ ...cellStyle, color: "#9ca3af" }}>
                {displayVal === "" && isDirty ? "(empty)" : "∅"}
              </span>
            );
          }
          return <span style={cellStyle}>{String(displayVal)}</span>;
        },
      };
    }) ?? [];

  const dataSource = (result?.rows ?? []).map((r, i) => ({ key: i, ...r }));

  // 筛选面板
  const filterPanel = (
    <div style={{ width: 640 }}>
      {draftFilters.length === 0 ? (
        <Typography.Text type="secondary">没有筛选条件，点击「添加条件」开始过滤。</Typography.Text>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {draftFilters.map((f, idx) => {
            const meta = columns.find((c) => c.name === f.column);
            const isBool = !!meta && isBoolLike(meta.data_type);
            const opsForCol = isBool ? BOOL_OPS : OPS;
            const needsValue = f.op !== "IS NULL" && f.op !== "IS NOT NULL";
            const isList = f.op === "IN" || f.op === "NOT IN";
            const isContains = f.op === "contains";
            return (
              <div
                key={f.id}
                style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "nowrap" }}
              >
                <Typography.Text type="secondary" style={{ width: 28, textAlign: "right" }}>
                  {idx === 0 ? "WHERE" : "AND"}
                </Typography.Text>
                <Select
                  showSearch
                  size="small"
                  style={{ width: 200, flexShrink: 0 }}
                  value={f.column}
                  options={columnOptions}
                  onChange={(v) => updateFilter(f.id, { column: v })}
                  optionFilterProp="label"
                />
                <Select
                  size="small"
                  style={{ width: 110, flexShrink: 0 }}
                  value={f.op}
                  options={opsForCol.map((o) => ({ label: o, value: o }))}
                  onChange={(v) => updateFilter(f.id, { op: v as Op })}
                />
                {needsValue ? (
                  isBool ? (
                    <Select
                      size="small"
                      style={{ flex: 1 }}
                      value={f.value || undefined}
                      placeholder="选择 true 或 false"
                      options={[
                        { label: "true", value: "true" },
                        { label: "false", value: "false" },
                      ]}
                      onChange={(v) => updateFilter(f.id, { value: v })}
                    />
                  ) : (
                    <Input
                      size="small"
                      style={{ flex: 1 }}
                      placeholder={
                        isList
                          ? "逗号分隔多个值，如 1,2,3"
                          : isContains
                          ? "包含的子串（会自动转为 text 后模糊匹配）"
                          : meta && isNumericLike(meta.data_type)
                          ? "数值"
                          : "文本 (LIKE 支持 % 通配符)"
                      }
                      value={f.value}
                      onChange={(e) => updateFilter(f.id, { value: e.target.value })}
                      onPressEnter={applyFilters}
                    />
                  )
                ) : (
                  <div style={{ flex: 1 }} />
                )}
                <Button
                  size="small"
                  type="text"
                  danger
                  icon={<CloseOutlined />}
                  onClick={() => removeFilter(f.id)}
                />
              </div>
            );
          })}
        </div>
      )}
      <div style={{ marginTop: 12, display: "flex", gap: 8, justifyContent: "space-between" }}>
        <Button size="small" icon={<PlusOutlined />} onClick={addFilter}>
          添加条件
        </Button>
        <Space>
          <Button size="small" onClick={clearFilters}>
            清空
          </Button>
          <Button size="small" type="primary" onClick={applyFilters}>
            应用
          </Button>
        </Space>
      </div>
    </div>
  );

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div className="sql-toolbar">
        <Space>
          <strong>
            {tab.database && <span style={{ color: "#2563eb" }}>{tab.database}/</span>}
            {tab.schema}.{tab.table}
          </strong>
          <Tag color={tab.role === "owner" ? "orange" : tab.role === "editor" ? "green" : "default"}>
            {tab.role}
          </Tag>
          <Button size="small" icon={<ReloadOutlined />} onClick={reload} loading={loading}>
            刷新
          </Button>
          <Popover
            trigger="click"
            placement="bottomLeft"
            open={filterOpen}
            onOpenChange={(o) => {
              setFilterOpen(o);
              if (o && draftFilters.length === 0 && appliedFilters.length === 0) {
                addFilter();
              } else if (o) {
                // 打开时将草稿重置为已应用的版本，便于继续编辑
                setDraftFilters(appliedFilters.map((f) => ({ ...f })));
              }
            }}
            content={filterPanel}
          >
            <Button
              size="small"
              icon={<FilterOutlined />}
              type={appliedFilters.length > 0 ? "primary" : "default"}
            >
              筛选
              {appliedFilters.length > 0 && ` (${appliedFilters.length})`}
            </Button>
          </Popover>
          {sortColumn && sortDir && (
            <Tag
              color="blue"
              closable
              onClose={(e) => {
                e.preventDefault();
                setSortColumn(null);
                setSortDir(null);
                setPage(1);
                fetchPage(1, pageSize, appliedFilters);
              }}
            >
              排序: {sortColumn} {sortDir === "asc" ? "↑" : "↓"}
            </Tag>
          )}
          <Tooltip title="在 SQL 窗口编辑 SELECT 语句">
            <Button
              size="small"
              icon={<FileSearchOutlined />}
              onClick={() =>
                onOpenSQL(
                  `SELECT * FROM ${fullTable} ${buildSuffix(
                    appliedFilters,
                    true
                  )};`
                )
              }
            >
              在 SQL 窗口打开
            </Button>
          </Tooltip>
          <Tooltip title="查看建表语句 (DDL)">
            <Button size="small" icon={<CodeOutlined />} onClick={openDDL}>
              查看 DDL
            </Button>
          </Tooltip>
        </Space>
        {result?.truncated && (
          <Tag color="orange" style={{ marginLeft: 8 }}>
            结果已截断
          </Tag>
        )}
        {total !== null && (
          <Tag color="default" style={{ marginLeft: "auto" }}>
            共 {total} 行
          </Tag>
        )}
      </div>
      <div style={{ flex: 1, overflow: "auto", padding: 12 }}>
        <Table
          size="small"
          bordered
          columns={tableColumns}
          dataSource={dataSource}
          loading={loading}
          pagination={{
            current: page,
            pageSize,
            showSizeChanger: true,
            pageSizeOptions: [20, 50, 100, 200, 500],
            total: total ?? undefined,
            showTotal: (t) => `共 ${t} 行`,
            onChange: (nextPage, nextPageSize) => {
              const sizeChanged = nextPageSize !== pageSize;
              const targetPage = sizeChanged ? 1 : nextPage;
              setPage(targetPage);
              setPageSize(nextPageSize);
              fetchPage(targetPage, nextPageSize, appliedFilters);
            },
          }}
          scroll={{ x: "max-content" }}
        />
      </div>
      {canEdit && hasPK && dirtyCount > 0 && (
        <div
          style={{
            padding: "6px 12px",
            borderTop: "1px solid #e5e7eb",
            background: "#fffbeb",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            flexShrink: 0,
          }}
        >
          <Space>
            <Button size="small" icon={<UndoOutlined />} onClick={discardChanges}>
              Discard Changes
            </Button>
            <Tag color="orange">{dirtyCount} edited row{dirtyCount > 1 ? "s" : ""}</Tag>
          </Space>
          <Space>
            <Button size="small" icon={<EyeOutlined />} onClick={() => setSqlPreviewOpen(true)}>
              SQL Preview
            </Button>
            <Button
              size="small"
              type="primary"
              icon={<SaveOutlined />}
              loading={saving}
              onClick={saveChanges}
            >
              Save Changes
            </Button>
          </Space>
        </div>
      )}
      {canEdit && !hasPK && dirtyCount === 0 && (
        <div
          style={{
            padding: "4px 12px",
            borderTop: "1px solid #e5e7eb",
            background: "#fafbfc",
            flexShrink: 0,
          }}
        >
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            该表无主键，不支持内联编辑。请在 SQL 窗口手动执行 UPDATE。
          </Typography.Text>
        </div>
      )}
      <Modal
        title="SQL Preview"
        open={sqlPreviewOpen}
        onCancel={() => setSqlPreviewOpen(false)}
        width={780}
        footer={[
          <Button
            key="copy"
            icon={<CopyOutlined />}
            onClick={() => {
              navigator.clipboard.writeText(generateUpdateSQL().join("\n"));
              message.success("已复制 SQL");
            }}
          >
            复制
          </Button>,
          <Button key="open" onClick={() => {
            onOpenSQL(generateUpdateSQL().join("\n"));
            setSqlPreviewOpen(false);
          }}>
            在 SQL 窗口打开
          </Button>,
          <Button
            key="exec"
            type="primary"
            icon={<SaveOutlined />}
            loading={saving}
            onClick={async () => {
              await saveChanges();
              setSqlPreviewOpen(false);
            }}
          >
            执行并保存
          </Button>,
        ]}
      >
        <pre
          style={{
            background: "#0f172a",
            color: "#e2e8f0",
            padding: 12,
            borderRadius: 4,
            maxHeight: 480,
            overflow: "auto",
            fontSize: 12,
            margin: 0,
            whiteSpace: "pre-wrap",
          }}
        >
          {generateUpdateSQL().join("\n") || "(无变更)"}
        </pre>
      </Modal>
      <Modal
        title={`DDL · ${tab.schema}.${tab.table}`}
        open={ddlOpen}
        onCancel={() => setDdlOpen(false)}
        width={780}
        footer={[
          <Button
            key="copy"
            icon={<CopyOutlined />}
            onClick={() => {
              navigator.clipboard.writeText(ddl);
              message.success("已复制 DDL");
            }}
          >
            复制
          </Button>,
          <Button key="close" type="primary" onClick={() => setDdlOpen(false)}>
            关闭
          </Button>,
        ]}
      >
        {ddlLoading ? (
          <div style={{ padding: 24, textAlign: "center" }}>加载中...</div>
        ) : (
          <pre
            style={{
              background: "#0f172a",
              color: "#e2e8f0",
              padding: 12,
              borderRadius: 4,
              maxHeight: 480,
              overflow: "auto",
              fontSize: 12,
              margin: 0,
            }}
          >
            {ddl || "(无)"}
          </pre>
        )}
      </Modal>
    </div>
  );
}
