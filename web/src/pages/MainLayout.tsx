import { useDeferredValue, useEffect, useMemo, useState, useCallback, useRef } from "react";
import {
  App,
  Button,
  Dropdown,
  Empty,
  Input,
  Modal,
  Space,
  Tree,
  Typography,
  Spin,
  Tooltip,
  Tabs,
} from "antd";
import {
  DatabaseOutlined,
  FolderFilled,
  FolderOpenFilled,
  TableOutlined,
  PlusOutlined,
  TeamOutlined,
  RobotOutlined,
  StarOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SettingOutlined,
  EyeOutlined,
  PartitionOutlined,
  FileSearchOutlined,
  UserOutlined,
  CloudDownloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import type { DataNode } from "antd/es/tree";
import { useNavigate } from "react-router-dom";
import { api } from "../api";
import type { Connection, Group, Role, TableInfo } from "../types";
import { canDDL, canManageGroup } from "../utils/role";
import { useAuth } from "../store";
import GroupManageModal from "../components/GroupManageModal";
import ConnectionFormModal from "../components/ConnectionFormModal";
import LLMConfigModal from "../components/LLMConfigModal";
import MySavedQueriesModal from "../components/MySavedQueriesModal";
import AuditLogModal from "../components/AuditLogModal";
import TaskListModal from "../components/TaskListModal";
import SQLTab from "../components/SQLTab";
import TableDataTab from "../components/TableDataTab";

interface TreeKey {
  type: "group" | "connection" | "database" | "schema" | "table";
  groupID?: number;
  connID?: number;
  database?: string;
  schema?: string;
  table?: string;
  kind?: string;
}

interface StickyTreeRow {
  key: string;
  type: "connection" | "database" | "schema";
  label: string;
}

function encodeKey(k: TreeKey): string {
  return JSON.stringify(k);
}

function decodeKey(s: string): TreeKey {
  return JSON.parse(s);
}

export interface OpenedTab {
  key: string;
  title: string;
  type: "sql" | "table";
  connID: number;
  connName: string;
  role: Role;
  driver?: string;
  database?: string;
  schema?: string;
  table?: string;
  initialSQL?: string;
}

export default function MainLayout() {
  const { user, llmConfigured, logout, loadMe } = useAuth();
  const { message, modal } = App.useApp();
  const nav = useNavigate();

  const [groups, setGroups] = useState<Group[]>([]);
  const [groupConns, setGroupConns] = useState<Record<number, Connection[]>>({});
  const [databasesMap, setDatabasesMap] = useState<Record<number, string[]>>({});
  const [schemasMap, setSchemasMap] = useState<Record<string, string[]>>({});
  const [tablesMap, setTablesMap] = useState<Record<string, TableInfo[]>>({});
  const [tableSearch, setTableSearch] = useState("");
  const deferredTableSearch = useDeferredValue(tableSearch.trim().toLocaleLowerCase());
  const [expanded, setExpanded] = useState<string[]>([]);
  const [loadingNode, setLoadingNode] = useState<string | null>(null);
  const [stickyTreeRows, setStickyTreeRows] = useState<StickyTreeRow[]>([]);
  const sidebarTreeScrollRef = useRef<HTMLDivElement | null>(null);
  const stickyFrameRef = useRef<number | null>(null);
  const [treeHeight, setTreeHeight] = useState(400);

  const SIDEBAR_WIDTH_KEY = "chat2db.sidebar.width";
  const SIDEBAR_MIN = 200;
  const SIDEBAR_MAX = 720;
  const [sidebarWidth, setSidebarWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return Number.isFinite(saved) && saved >= SIDEBAR_MIN && saved <= SIDEBAR_MAX ? saved : 300;
  });
  const [resizing, setResizing] = useState(false);
  const resizingRef = useRef(false);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!resizingRef.current) return;
      const next = Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, e.clientX));
      setSidebarWidth(next);
    };
    const onUp = () => {
      if (!resizingRef.current) return;
      resizingRef.current = false;
      setResizing(false);
      document.body.classList.remove("is-resizing");
      setSidebarWidth((w) => {
        localStorage.setItem(SIDEBAR_WIDTH_KEY, String(w));
        return w;
      });
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    resizingRef.current = true;
    setResizing(true);
    document.body.classList.add("is-resizing");
  };

  const [groupModal, setGroupModal] = useState(false);
  const [llmModal, setLLMModal] = useState(false);
  const [savedModal, setSavedModal] = useState(false);
  const [auditModal, setAuditModal] = useState(false);
  const [taskModal, setTaskModal] = useState(false);
  const [connModal, setConnModal] = useState<{ open: boolean; groupID?: number; editing?: Connection } | null>(null);

  const [tabs, setTabs] = useState<OpenedTab[]>([]);
  const [activeTab, setActiveTab] = useState<string | undefined>();

  const reloadGroups = useCallback(async () => {
    try {
      const gs = await api.listGroups();
      setGroups(gs);
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载组列表失败");
    }
  }, [message]);

  useEffect(() => {
    reloadGroups();
  }, [reloadGroups]);

  const loadConnections = async (groupID: number) => {
    const cs = await api.listConnections(groupID);
    setGroupConns((m) => ({ ...m, [groupID]: cs }));
  };

  const loadDatabases = async (connID: number) => {
    const dbs = await api.listDatabases(connID);
    setDatabasesMap((m) => ({ ...m, [connID]: dbs.map((d) => d.name) }));
  };

  const loadSchemas = async (connID: number, database: string) => {
    const ss = await api.listSchemas(connID, database);
    setSchemasMap((m) => ({ ...m, [`${connID}/${database}`]: ss.map((s) => s.name) }));
  };

  const loadTables = async (connID: number, database: string, schema: string) => {
    const ts = await api.listTables(connID, schema, database);
    setTablesMap((m) => ({ ...m, [`${connID}/${database}/${schema}`]: ts }));
  };

  const treeData: DataNode[] = useMemo(() => {
    const filter = deferredTableSearch;
    return groups.flatMap<DataNode>((g) => {
      const connections = (groupConns[g.id] ?? []).flatMap<DataNode>((c) => {
        const databases = (databasesMap[c.id] ?? []).flatMap<DataNode>((db) => {
          const schemas = (schemasMap[`${c.id}/${db}`] ?? []).flatMap<DataNode>((s) => {
            const tables = (tablesMap[`${c.id}/${db}/${s}`] ?? [])
              .filter((t) => !filter || t.name.toLocaleLowerCase().includes(filter))
              .map<DataNode>((t) => ({
                key: encodeKey({
                  type: "table",
                  groupID: g.id,
                  connID: c.id,
                  database: db,
                  schema: s,
                  table: t.name,
                  kind: t.kind,
                }),
                title: t.name,
                icon: t.kind === "table" ? <TableOutlined className="tree-icon-table" /> : <EyeOutlined className="tree-icon-view" />,
                isLeaf: true,
              }));
            if (filter && tables.length === 0) return [];
            return [{
              key: encodeKey({ type: "schema", groupID: g.id, connID: c.id, database: db, schema: s }),
              title: s,
              icon: <PartitionOutlined className="tree-icon-schema" />,
              children: tables,
            }];
          });
          if (filter && schemas.length === 0) return [];
          return [{
            key: encodeKey({ type: "database", groupID: g.id, connID: c.id, database: db }),
            title: db,
            icon: <DatabaseOutlined style={{ color: db === c.database ? "#16a34a" : "#6b7280" }} />,
            children: schemas,
          }];
        });
        if (filter && databases.length === 0) return [];
        return [{
          key: encodeKey({ type: "connection", groupID: g.id, connID: c.id }),
          title: c.name,
          icon: <DatabaseOutlined style={{ color: "#2563eb" }} />,
          children: databases,
        }];
      });
      if (filter && connections.length === 0) return [];
      return [{
        key: encodeKey({ type: "group", groupID: g.id }),
        title: g.name,
        icon: <FolderFilled className="tree-icon-folder" />,
        children: connections,
      }];
    });
  }, [groups, groupConns, databasesMap, schemasMap, tablesMap, deferredTableSearch]);

  useEffect(() => {
    const element = sidebarTreeScrollRef.current;
    if (!element) return;
    const updateHeight = () => setTreeHeight(Math.max(100, Math.floor(element.clientHeight)));
    updateHeight();
    const observer = new ResizeObserver(updateHeight);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const searchExpandedKeys = useMemo(() => {
    if (!deferredTableSearch) return expanded;
    const keys: React.Key[] = [];
    const visit = (nodes: DataNode[]) => {
      for (const node of nodes) {
        if (node.children?.length) {
          keys.push(node.key);
          visit(node.children);
        }
      }
    };
    visit(treeData);
    return keys;
  }, [deferredTableSearch, expanded, treeData]);

  const updateStickyTreeRows = useCallback(() => {
    const scrollContainer = sidebarTreeScrollRef.current;
    if (!scrollContainer) return;

    const containerTop = scrollContainer.getBoundingClientRect().top;
    const markerRows = Array.from(
      scrollContainer.querySelectorAll<HTMLElement>("[data-tree-node-key]")
    )
      .map((marker) => {
        const row = marker.closest<HTMLElement>(".ant-tree-treenode");
        const encodedKey = marker.dataset.treeNodeKey;
        if (!row || !encodedKey) return null;
        try {
          return {
            key: encodedKey,
            node: decodeKey(encodedKey),
            top: row.getBoundingClientRect().top,
          };
        } catch {
          return null;
        }
      })
      .filter((item): item is { key: string; node: TreeKey; top: number } => item !== null)
      .sort((a, b) => a.top - b.top);

    const nodeAt = (top: number): TreeKey | null => {
      let current: TreeKey | null = null;
      for (const item of markerRows) {
        if (item.top > top) break;
        current = item.node;
      }
      return current;
    };

    const rowHeight = 28;
    const connectionNode = nodeAt(containerTop + 1);
    if (!connectionNode?.connID || !connectionNode.groupID) {
      setStickyTreeRows((previous) => (previous.length === 0 ? previous : []));
      return;
    }

    const connection = (groupConns[connectionNode.groupID] ?? []).find(
      (item) => item.id === connectionNode.connID
    );
    const nextRows: StickyTreeRow[] = [
      {
        key: encodeKey({
          type: "connection",
          groupID: connectionNode.groupID,
          connID: connectionNode.connID,
        }),
        type: "connection",
        label: connection?.name ?? `连接 ${connectionNode.connID}`,
      },
    ];

    const databaseNode = nodeAt(containerTop + rowHeight + 1);
    if (
      databaseNode?.connID === connectionNode.connID &&
      databaseNode.database
    ) {
      nextRows.push({
        key: encodeKey({
          type: "database",
          groupID: databaseNode.groupID,
          connID: databaseNode.connID,
          database: databaseNode.database,
        }),
        type: "database",
        label: databaseNode.database,
      });
    }

    const schemaNode = nodeAt(containerTop + rowHeight * 2 + 1);
    if (
      schemaNode?.connID === connectionNode.connID &&
      schemaNode.database === databaseNode?.database &&
      schemaNode.schema
    ) {
      nextRows.push({
        key: encodeKey({
          type: "schema",
          groupID: schemaNode.groupID,
          connID: schemaNode.connID,
          database: schemaNode.database,
          schema: schemaNode.schema,
        }),
        type: "schema",
        label: schemaNode.schema,
      });
    }

    setStickyTreeRows((previous) => {
      const unchanged =
        previous.length === nextRows.length &&
        previous.every((item, index) => item.key === nextRows[index].key);
      return unchanged ? previous : nextRows;
    });
  }, [groupConns]);

  const scheduleStickyTreeUpdate = useCallback(() => {
    if (stickyFrameRef.current !== null) return;
    stickyFrameRef.current = window.requestAnimationFrame(() => {
      stickyFrameRef.current = null;
      updateStickyTreeRows();
    });
  }, [updateStickyTreeRows]);

  useEffect(() => {
    scheduleStickyTreeUpdate();
    return () => {
      if (stickyFrameRef.current !== null) {
        window.cancelAnimationFrame(stickyFrameRef.current);
        stickyFrameRef.current = null;
      }
    };
  }, [treeData, expanded, sidebarWidth, scheduleStickyTreeUpdate]);

  const titleRender = useCallback((node: any) => {
    const k: TreeKey = JSON.parse(node.key);
    if (k.type === "group") {
      const g = groups.find((gg) => gg.id === k.groupID);
      if (!g) {
        return <span className="tree-node-marker" data-tree-node-key={node.key}>{node.title}</span>;
      }
      return (
        <span className="tree-conn-line tree-node-marker" data-tree-node-key={node.key}>
          <span>
            <Space size={6}>
              <strong>{g.name}</strong>
              <span className={`role-tag role-${g.role}`}>{g.role}</span>
            </Space>
          </span>
          <Space size={4}>
            <Tooltip title="管理组成员">
              <Button
                size="small"
                type="text"
                icon={<TeamOutlined />}
                onClick={(e) => {
                  e.stopPropagation();
                  openManageGroup(g);
                }}
              />
            </Tooltip>
            {canManageGroup(g.role) && (
              <Tooltip title="新建连接">
                <Button
                  size="small"
                  type="text"
                  icon={<PlusOutlined />}
                  onClick={(e) => {
                    e.stopPropagation();
                    setConnModal({ open: true, groupID: g.id });
                  }}
                />
              </Tooltip>
            )}
          </Space>
        </span>
      );
    }
    if (k.type === "connection") {
      const g = groups.find((gg) => gg.id === k.groupID);
      const c = (groupConns[k.groupID!] ?? []).find((cc) => cc.id === k.connID);
      if (!g || !c) {
        return <span className="tree-node-marker" data-tree-node-key={node.key}>{node.title}</span>;
      }
      return (
        <span className="tree-conn-line tree-node-marker" data-tree-node-key={node.key}>
          <span style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
            <Space size={6}>
              <span>{c.name}</span>
              <Tooltip title={`${c.host}:${c.port}/${c.database}`}>
                <Typography.Text
                  type="secondary"
                  style={{
                    fontSize: 12,
                    maxWidth: '200px',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    display: 'inline-block'
                  }}
                >
                  {c.host}:{c.port}/{c.database}
                </Typography.Text>
              </Tooltip>
            </Space>
          </span>
          <Space size={4}>
            <Tooltip title="新建 SQL 查询">
              <Button
                size="small"
                type="text"
                icon={<FileSearchOutlined />}
                onClick={(e) => {
                  e.stopPropagation();
                  openSQLTab(c, g.role);
                }}
              />
            </Tooltip>
            {(() => {
              const items: any[] = [
                { key: "refresh", label: "刷新数据库列表" },
              ];
              if (canManageGroup(g.role)) {
                items.push(
                  { key: "edit", label: "编辑连接" },
                  { key: "test", label: "测试连接" },
                  { key: "delete", danger: true, label: "删除连接" }
                );
              }
              return (
                <Dropdown
                  menu={{
                    items,
                    onClick: ({ key: action }) => onConnAction(action, c, g),
                  }}
                  trigger={["click"]}
                >
                  <Button
                    size="small"
                    type="text"
                    icon={<SettingOutlined />}
                    onClick={(e) => e.stopPropagation()}
                  />
                </Dropdown>
              );
            })()}
          </Space>
        </span>
      );
    }
    if (k.type === "table" && k.kind && k.kind !== "table") {
      return (
        <span className="tree-node-marker" data-tree-node-key={node.key}>
          {node.title}{" "}
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            ({k.kind})
          </Typography.Text>
        </span>
      );
    }
    return <span className="tree-node-marker" data-tree-node-key={node.key}>{node.title}</span>;
  }, [groups, groupConns]);

  const onLoadData = useCallback(async (node: any) => {
    const k = decodeKey(node.key);
    setLoadingNode(node.key);
    try {
      if (k.type === "group" && k.groupID && !groupConns[k.groupID]) {
        await loadConnections(k.groupID);
      } else if (k.type === "connection" && k.connID && !databasesMap[k.connID]) {
        await loadDatabases(k.connID);
      } else if (k.type === "database" && k.connID && k.database && !schemasMap[`${k.connID}/${k.database}`]) {
        await loadSchemas(k.connID, k.database);
      } else if (k.type === "schema" && k.connID && k.database && k.schema && !tablesMap[`${k.connID}/${k.database}/${k.schema}`]) {
        await loadTables(k.connID, k.database, k.schema);
      }
    } catch (e: any) {
      message.error(e?.response?.data?.error ?? "加载失败");
    } finally {
      setLoadingNode(null);
    }
  }, [groupConns, schemasMap, tablesMap, message]);

  const openSQLTab = useCallback((conn: Connection, role: Role, initialSQL?: string, database?: string) => {
    const key = `sql-${conn.id}-${Date.now()}`;
    const db = database || conn.database;
    const tab: OpenedTab = {
      key,
      title: `SQL · ${conn.name}/${db}`,
      type: "sql",
      connID: conn.id,
      connName: conn.name,
      role,
      driver: conn.driver,
      database: db,
      initialSQL,
    };
    setTabs((ts) => [...ts, tab]);
    setActiveTab(key);
  }, []);

  const openTableTab = useCallback((conn: Connection, role: Role, schema: string, table: string, database?: string) => {
    const db = database || conn.database;
    const key = `tbl-${conn.id}-${db}-${schema}-${table}`;
    setTabs((ts) => {
      if (ts.some((t) => t.key === key)) return ts;
      return [
        ...ts,
        {
          key,
          title: `${db}/${schema}.${table}`,
          type: "table",
          connID: conn.id,
          connName: conn.name,
          role,
          driver: conn.driver,
          database: db,
          schema,
          table,
        },
      ];
    });
    setActiveTab(key);
  }, []);

  const onSelectNode = useCallback(async (selected: React.Key[]) => {
    if (selected.length === 0) return;
    const k = decodeKey(selected[0] as string);
    if (k.type === "table" && k.connID && k.schema && k.table) {
      const group = groups.find((g) => g.id === k.groupID);
      const conn = (groupConns[k.groupID!] ?? []).find((c) => c.id === k.connID);
      if (!group || !conn) return;
      openTableTab(conn, group.role, k.schema, k.table, k.database);
    } else if (k.type === "connection" && k.connID) {
      const group = groups.find((g) => g.id === k.groupID);
      const conn = (groupConns[k.groupID!] ?? []).find((c) => c.id === k.connID);
      if (group && conn) openSQLTab(conn, group.role);
    } else if (k.type === "database" && k.connID && k.database) {
      const group = groups.find((g) => g.id === k.groupID);
      const conn = (groupConns[k.groupID!] ?? []).find((c) => c.id === k.connID);
      if (group && conn) openSQLTab(conn, group.role, undefined, k.database);
    }
  }, [groups, groupConns, openTableTab, openSQLTab]);

  const onConnAction = useCallback((key: string, conn: Connection, group: Group) => {
    if (key === "edit") setConnModal({ open: true, groupID: group.id, editing: conn });
    if (key === "refresh") {
      // 强制重新拉取该连接的数据库列表，并清除对应的 schemas/tables 缓存
      api
        .listDatabases(conn.id)
        .then((dbs) => {
          setDatabasesMap((m) => ({ ...m, [conn.id]: dbs.map((d) => d.name) }));
          setSchemasMap((m) => {
            const next = { ...m };
            for (const k of Object.keys(next)) {
              if (k.startsWith(`${conn.id}/`)) delete next[k];
            }
            return next;
          });
          setTablesMap((m) => {
            const next = { ...m };
            for (const k of Object.keys(next)) {
              if (k.startsWith(`${conn.id}/`)) delete next[k];
            }
            return next;
          });
          message.success(`已刷新，共 ${dbs.length} 个数据库`);
        })
        .catch((e) => message.error(e?.response?.data?.error ?? "刷新失败"));
    }
    if (key === "test") {
      api.testConnection({ connection_id: conn.id }).then((res) => {
        if (res.ok) message.success("连接成功");
        else message.error("连接失败：" + res.error);
      });
    }
    if (key === "delete") {
      modal.confirm({
        title: `删除连接 "${conn.name}" ?`,
        okType: "danger",
        onOk: async () => {
          await api.deleteConnection(conn.id);
          message.success("已删除");
          await loadConnections(group.id);
          setDatabasesMap((m) => {
            const { [conn.id]: _, ...rest } = m;
            return rest;
          });
          setSchemasMap((m) => {
            const next = { ...m };
            for (const k of Object.keys(next)) {
              if (k.startsWith(`${conn.id}/`)) delete next[k];
            }
            return next;
          });
          setTablesMap((m) => {
            const next = { ...m };
            for (const k of Object.keys(next)) {
              if (k.startsWith(`${conn.id}/`)) delete next[k];
            }
            return next;
          });
        },
      });
    }
  }, [message, modal]);

  const openManageGroup = useCallback((_g: Group) => {
    setGroupModal(true);
  }, []);

  const removeTab = (key: string) => {
    setTabs((ts) => {
      const next = ts.filter((t) => t.key !== key);
      if (activeTab === key) {
        setActiveTab(next.length ? next[next.length - 1].key : undefined);
      }
      return next;
    });
  };

  const findConnAndGroup = (connID: number): { conn: Connection; group: Group } | null => {
    for (const g of groups) {
      const cs = groupConns[g.id] ?? [];
      const c = cs.find((cc) => cc.id === connID);
      if (c) return { conn: c, group: g };
    }
    return null;
  };

  // 仅当用户在任意组是 admin/owner 时才显示"审计日志"入口。
  // 后端会按组隔离做二次校验，这里只是 UI 层的可见性提示。
  const canViewAudit = useMemo(() => groups.some((g) => canDDL(g.role)), [groups]);

  return (
    <div className="app-shell">
      <div className="app-header">
        <Space size={16}>
          <span className="brand">🗄️ Chat2DB Web</span>
        </Space>
        <Space size={8}>
          <Tooltip title="刷新组列表">
            <Button type="text" style={{ color: "#fff" }} icon={<ReloadOutlined />} onClick={reloadGroups} />
          </Tooltip>
          <Button type="text" style={{ color: "#fff" }} icon={<PlusOutlined />} onClick={() => setGroupModal(true)}>
            连接组
          </Button>
          <Button
            type="text"
            style={{ color: llmConfigured ? "#52c41a" : "#fff" }}
            icon={<RobotOutlined />}
            onClick={() => setLLMModal(true)}
          >
            AI 配置 {llmConfigured && "✓"}
          </Button>
          <Button type="text" style={{ color: "#fff" }} icon={<StarOutlined />} onClick={() => setSavedModal(true)}>
            我的收藏
          </Button>
          {canViewAudit && (
            <Button
              type="text"
              style={{ color: "#fff" }}
              icon={<FileSearchOutlined />}
              onClick={() => setAuditModal(true)}
            >
              审计日志
            </Button>
          )}
          <Button
            type="text"
            style={{ color: "#fff" }}
            icon={<CloudDownloadOutlined />}
            onClick={() => setTaskModal(true)}
          >
            任务
          </Button>
          <Dropdown
            menu={{
              items: [
                { key: "name", label: <span>{user?.name} ({user?.email})</span>, disabled: true },
                { type: "divider" },
                { key: "logout", icon: <PoweroffOutlined />, label: "退出登录" },
              ],
              onClick: ({ key }) => {
                if (key === "logout") logout();
              },
            }}
          >
            <Button type="text" style={{ color: "#fff" }} icon={<UserOutlined />}>
              {user?.name}
            </Button>
          </Dropdown>
        </Space>
      </div>
      <div className="app-main">
        <div className="app-sidebar" style={{ width: sidebarWidth }}>
          <div className="sidebar-section">
            <div className="sidebar-section-title">
              <span>连接组</span>
              <Space size={2}>
                <Tooltip title="新建组">
                  <Button size="small" type="text" icon={<PlusOutlined />} onClick={() => setGroupModal(true)} />
                </Tooltip>
              </Space>
            </div>
          </div>
          <div className="sidebar-table-search">
            <Input
              size="small"
              allowClear
              prefix={<SearchOutlined />}
              placeholder="搜索已加载的表或视图"
              value={tableSearch}
              onChange={(event) => setTableSearch(event.target.value)}
            />
          </div>
          <div className="sidebar-tree-wrapper">
            {!deferredTableSearch && stickyTreeRows.length > 0 && (
              <div className="sidebar-tree-sticky" aria-hidden="true">
                {stickyTreeRows.map((item, index) => (
                  <div
                    key={item.key}
                    className={`sidebar-tree-sticky-row sticky-${item.type}`}
                    style={{ paddingLeft: 12 + index * 20 }}
                  >
                    {item.type === "schema" ? (
                      <PartitionOutlined className="tree-icon-schema" />
                    ) : (
                      <DatabaseOutlined
                        style={{ color: item.type === "connection" ? "#2563eb" : "#6b7280" }}
                      />
                    )}
                    <span>{item.label}</span>
                  </div>
                ))}
              </div>
            )}
            <div
              ref={sidebarTreeScrollRef}
              className="sidebar-tree-scroll"
              onScroll={scheduleStickyTreeUpdate}
            >
              {groups.length === 0 ? (
                <Empty
                  description="还没有连接组"
                  imageStyle={{ height: 40 }}
                  style={{ marginTop: 24 }}
                >
                  <Button type="primary" size="small" icon={<PlusOutlined />} onClick={() => setGroupModal(true)}>
                    新建第一个组
                  </Button>
                </Empty>
              ) : (
                <Spin spinning={!!loadingNode} size="small">
                  <Tree
                    showIcon
                    virtual
                    height={treeHeight}
                    treeData={treeData}
                    titleRender={titleRender}
                    expandedKeys={searchExpandedKeys}
                    onExpand={(keys) => setExpanded(keys as string[])}
                    onScroll={scheduleStickyTreeUpdate}
                    loadData={onLoadData}
                    onSelect={onSelectNode}
                    blockNode
                  />
                </Spin>
              )}
            </div>
          </div>
          <div
            className={`sidebar-resizer${resizing ? " resizing" : ""}`}
            onMouseDown={startResize}
            onDoubleClick={() => {
              setSidebarWidth(300);
              localStorage.setItem(SIDEBAR_WIDTH_KEY, "300");
            }}
            title="拖拽调整宽度，双击恢复默认"
          />
        </div>
        <div className="app-content">
          {tabs.length === 0 ? (
            <div className="empty-state">
              <Empty description="点击左侧连接或表打开查询窗口" />
            </div>
          ) : (
            <Tabs
              type="editable-card"
              hideAdd
              activeKey={activeTab}
              onChange={setActiveTab}
              onEdit={(key, action) => {
                if (action === "remove" && typeof key === "string") removeTab(key);
              }}
              style={{ height: "100%" }}
              items={tabs.map((t) => ({
                key: t.key,
                label: t.title,
                children: (
                  <div className="sql-tab-wrapper">
                    {t.type === "sql" ? (
                      <SQLTab tab={t} />
                    ) : (
                      <TableDataTab
                        tab={t}
                        onOpenSQL={(sql) => {
                          const found = findConnAndGroup(t.connID);
                          if (found) openSQLTab(found.conn, found.group.role, sql, t.database);
                        }}
                      />
                    )}
                  </div>
                ),
              }))}
            />
          )}
        </div>
      </div>

      <GroupManageModal
        open={groupModal}
        groups={groups}
        onClose={() => setGroupModal(false)}
        onChanged={async () => {
          await reloadGroups();
        }}
      />

      <ConnectionFormModal
        open={connModal?.open ?? false}
        groupID={connModal?.groupID}
        editing={connModal?.editing}
        onClose={() => setConnModal(null)}
        onSaved={async () => {
          if (connModal?.groupID) {
            await loadConnections(connModal.groupID);
          }
          setConnModal(null);
        }}
      />

      <LLMConfigModal
        open={llmModal}
        currentEndpoint={user?.llm_endpoint}
        currentModel={user?.llm_model}
        onClose={() => setLLMModal(false)}
        onSaved={async () => {
          await loadMe();
          setLLMModal(false);
        }}
      />

      <MySavedQueriesModal
        open={savedModal}
        onClose={() => setSavedModal(false)}
        onJump={(connID, sql) => {
          let foundConn: Connection | undefined;
          let foundGroup: Group | undefined;
          for (const g of groups) {
            const cs = groupConns[g.id] ?? [];
            const c = cs.find((cc) => cc.id === connID);
            if (c) {
              foundConn = c;
              foundGroup = g;
              break;
            }
          }
          if (!foundConn || !foundGroup) {
            message.warning("请先在左侧展开对应的组以加载连接");
            return;
          }
          openSQLTab(foundConn, foundGroup.role, sql);
          setSavedModal(false);
        }}
      />

      <AuditLogModal
        open={auditModal}
        groups={groups}
        onClose={() => setAuditModal(false)}
      />

      <TaskListModal
        open={taskModal}
        groups={groups}
        onClose={() => setTaskModal(false)}
      />
    </div>
  );
}
