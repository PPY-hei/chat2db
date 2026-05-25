// 表结构编辑 → ALTER/CREATE/DROP SQL 生成器（按 postgres / mysql 方言）。
// 纯函数，不依赖任何运行时状态，便于单测与 SQL Preview。

export interface ColumnDraft {
  key: string; // 行唯一 key（前端用）
  origName?: string; // 已存在列的原始列名；新增列为空
  name: string;
  data_type: string;
  nullable: boolean;
  default_value: string | null; // 原始 SQL 表达式（如 '0' / 'now()' / "'abc'"）
  comment: string | null;
  is_primary: boolean; // 只读展示
  auto_increment: boolean; // 只读展示（MySQL MODIFY 时需保留）
  _deleted?: boolean;
}

export interface IndexDraft {
  key: string;
  origName?: string; // 已存在索引的原始名；新增索引为空
  name: string;
  columns: string[];
  unique: boolean;
  primary: boolean; // 主键索引，不可删
  _deleted?: boolean;
}

type Driver = "postgres" | "mysql" | string;

function quoteIdent(driver: Driver, id: string): string {
  if (driver === "mysql") {
    return "`" + id.replace(/`/g, "``") + "`";
  }
  return '"' + id.replace(/"/g, '""') + '"';
}

function qualifiedTable(driver: Driver, schema: string, table: string): string {
  return `${quoteIdent(driver, schema)}.${quoteIdent(driver, table)}`;
}

// 构造单列定义片段：<ident> <type> [NOT NULL] [DEFAULT x] [AUTO_INCREMENT] [COMMENT '...']
// PG 不在列定义里放 COMMENT（单独用 COMMENT ON），故 withComment 仅 MySQL 用。
function columnDef(driver: Driver, col: ColumnDraft, withComment: boolean): string {
  let s = `${quoteIdent(driver, col.name)} ${col.data_type}`;
  if (!col.nullable) s += " NOT NULL";
  if (col.default_value != null && col.default_value !== "") {
    s += ` DEFAULT ${col.default_value}`;
  }
  if (driver === "mysql" && col.auto_increment) {
    s += " AUTO_INCREMENT";
  }
  if (withComment && col.comment) {
    s += ` COMMENT ${sqlString(col.comment)}`;
  }
  return s;
}

function sqlString(s: string): string {
  return "'" + s.replace(/'/g, "''") + "'";
}

function colChanged(a: ColumnDraft, b: ColumnDraft): boolean {
  return (
    a.name !== b.name ||
    a.data_type !== b.data_type ||
    a.nullable !== b.nullable ||
    (a.default_value ?? "") !== (b.default_value ?? "") ||
    (a.comment ?? "") !== (b.comment ?? "")
  );
}

/**
 * 对比原始结构与编辑态，生成 SQL 语句数组。
 * 顺序：加列 → 改列 → 删索引 → 删列 → 建索引（兼顾依赖关系）。
 */
export function buildAlterStatements(
  driver: Driver,
  schema: string,
  table: string,
  origCols: ColumnDraft[],
  draftCols: ColumnDraft[],
  origIdx: IndexDraft[],
  draftIdx: IndexDraft[]
): string[] {
  const T = qualifiedTable(driver, schema, table);
  const stmts: string[] = [];
  const origColByName = new Map(origCols.map((c) => [c.origName ?? c.name, c]));

  // 1. 加列
  for (const col of draftCols) {
    if (col._deleted || col.origName) continue;
    if (!col.name || !col.data_type) continue;
    stmts.push(`ALTER TABLE ${T} ADD COLUMN ${columnDef(driver, col, driver === "mysql")}`);
    if (driver !== "mysql" && col.comment) {
      stmts.push(
        `COMMENT ON COLUMN ${T}.${quoteIdent(driver, col.name)} IS ${sqlString(col.comment)}`
      );
    }
  }

  // 2. 改列（已存在、未删除、有变更）
  for (const col of draftCols) {
    if (col._deleted || !col.origName) continue;
    const orig = origColByName.get(col.origName);
    if (!orig || !colChanged(orig, col)) continue;

    if (driver === "mysql") {
      // MySQL: CHANGE（含改名）/ MODIFY，一条语句重写整列定义
      if (col.name !== col.origName) {
        stmts.push(
          `ALTER TABLE ${T} CHANGE COLUMN ${quoteIdent(driver, col.origName)} ${columnDef(driver, col, true)}`
        );
      } else {
        stmts.push(`ALTER TABLE ${T} MODIFY COLUMN ${columnDef(driver, col, true)}`);
      }
    } else {
      // PG: 拆成多条；改名先行，后续用新名
      if (col.name !== col.origName) {
        stmts.push(
          `ALTER TABLE ${T} RENAME COLUMN ${quoteIdent(driver, col.origName)} TO ${quoteIdent(driver, col.name)}`
        );
      }
      const cn = quoteIdent(driver, col.name);
      if (orig.data_type !== col.data_type) {
        stmts.push(`ALTER TABLE ${T} ALTER COLUMN ${cn} TYPE ${col.data_type}`);
      }
      if (orig.nullable !== col.nullable) {
        stmts.push(
          `ALTER TABLE ${T} ALTER COLUMN ${cn} ${col.nullable ? "DROP NOT NULL" : "SET NOT NULL"}`
        );
      }
      if ((orig.default_value ?? "") !== (col.default_value ?? "")) {
        if (col.default_value != null && col.default_value !== "") {
          stmts.push(`ALTER TABLE ${T} ALTER COLUMN ${cn} SET DEFAULT ${col.default_value}`);
        } else {
          stmts.push(`ALTER TABLE ${T} ALTER COLUMN ${cn} DROP DEFAULT`);
        }
      }
      if ((orig.comment ?? "") !== (col.comment ?? "")) {
        const val = col.comment ? sqlString(col.comment) : "NULL";
        stmts.push(`COMMENT ON COLUMN ${T}.${cn} IS ${val}`);
      }
    }
  }

  // 3. 删索引（在删列之前，避免依赖冲突）
  for (const idx of draftIdx) {
    if (!idx._deleted || !idx.origName || idx.primary) continue;
    if (driver === "mysql") {
      stmts.push(`ALTER TABLE ${T} DROP INDEX ${quoteIdent(driver, idx.origName)}`);
    } else {
      stmts.push(`DROP INDEX ${quoteIdent(driver, schema)}.${quoteIdent(driver, idx.origName)}`);
    }
  }

  // 4. 删列
  for (const col of draftCols) {
    if (!col._deleted || !col.origName) continue;
    stmts.push(`ALTER TABLE ${T} DROP COLUMN ${quoteIdent(driver, col.origName)}`);
  }

  // 5. 建索引（新增的）
  for (const idx of draftIdx) {
    if (idx._deleted || idx.origName || idx.primary) continue;
    if (!idx.name || idx.columns.length === 0) continue;
    const cols = idx.columns.map((c) => quoteIdent(driver, c)).join(", ");
    const uniq = idx.unique ? "UNIQUE " : "";
    if (driver === "mysql") {
      stmts.push(`CREATE ${uniq}INDEX ${quoteIdent(driver, idx.name)} ON ${T} (${cols})`);
    } else {
      stmts.push(`CREATE ${uniq}INDEX ${quoteIdent(driver, idx.name)} ON ${T} (${cols})`);
    }
  }

  return stmts;
}
