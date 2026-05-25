package dbexec

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/chy/chat2db/server/internal/model"
)

// Driver 是所有数据源适配器必须实现的统一接口。
//
// 设计原则：
//   - 一个 Driver 实例持有单个 *model.Connection 视角，以及由此派生的连接池引用。
//   - 元数据方法保持 context-first 签名，便于上层注入超时/取消/审计。
//   - WithDatabase 返回一个新的 Driver（不改变当前实例），用于临时切换数据库浏览。
//   - Invalidate 用于连接配置变更/删除后，显式清理全部相关资源（含 SSH 隧道）。
//
// 注：本接口只暴露 dbexec 已经稳定的能力，不为 PR #1 引入新行为。
type Driver interface {
	// Name 返回驱动名（"postgres" / "mysql" / ...）。
	Name() string

	// Capabilities 声明驱动的静态能力，供前端动态渲染连接表单。
	Capabilities() Capabilities

	// WithDatabase 返回一个指向同一物理连接但 Database 字段被替换的新 Driver。
	// 用于临时切换数据库浏览，不会修改持久化的连接配置。
	WithDatabase(database string) Driver

	// Ping 不经过池缓存做一次连通性探测。
	Ping(ctx context.Context) error

	// Exec 执行一条 SQL 并返回结果集（或影响行数）。
	Exec(ctx context.Context, sql string, args ...any) (*QueryResult, error)

	// ListDatabases 返回实例上的所有数据库。
	ListDatabases(ctx context.Context) ([]DatabaseInfo, error)

	// ListSchemas 返回当前数据库中的所有 schema。
	ListSchemas(ctx context.Context) ([]Schema, error)

	// ListTables 返回指定 schema 下的表/视图。
	ListTables(ctx context.Context, schema string) ([]TableInfo, error)

	// ListColumns 返回指定表的列信息。
	ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error)

	// ListIndexes 返回指定表的索引信息。
	ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error)

	// GenerateTableDDL 返回表的可读 DDL。
	GenerateTableDDL(ctx context.Context, schema, table string) (string, error)

	// Invalidate 关闭并清理与本连接相关的全部资源（含 SSH 隧道）。
	// 实现必须幂等、并发安全。
	Invalidate()
}

// Capabilities 描述驱动的静态能力，用于前端动态构建连接表单
// 以及后端做能力裁剪。所有字段都应是对最终用户可见的、与驱动无关的语义。
type Capabilities struct {
	// DefaultPort 是驱动的默认端口（用于前端占位）。
	DefaultPort int `json:"default_port"`

	// SSLModes 列出驱动支持的 SSL 模式（如 "disable", "require", "verify-ca" ...）。
	// 顺序会被前端按原序展示。
	SSLModes []string `json:"ssl_modes"`

	// SupportsSSH 表示驱动是否可以跑在 SSH 隧道之上。
	SupportsSSH bool `json:"supports_ssh"`

	// SupportsProxy 表示驱动是否可以经 HTTP/SOCKS5 代理拨号。
	SupportsProxy bool `json:"supports_proxy"`

	// SupportsMTLS 表示驱动是否支持双向 TLS（客户端证书 + 私钥）。
	SupportsMTLS bool `json:"supports_mtls"`

	// SchemaSupport 表示驱动是否区分 Database 和 Schema 两级。
	// true:  PostgreSQL 等，database 下还有 schema。
	// false: MySQL 等，database 本身即 schema。
	SchemaSupport bool `json:"schema_support"`
}

// Factory 根据 *model.Connection 构造一个 Driver 实例。
//
// 返回 error 是为了让不合法的连接配置在 Open 阶段就失败，
// 避免把错误延迟到第一次 SQL 执行时才暴露。
type Factory func(c *model.Connection) (Driver, error)

// ErrUnsupportedDriver 表示 Connection.Driver 没有对应的注册实现。
// 调用方可以用 errors.Is 识别该错误，以便区分"用户选错驱动"和"驱动内部错误"。
var ErrUnsupportedDriver = errors.New("dbexec: unsupported driver")

// registry 存放 name -> Factory 的映射。
// 各驱动文件在自身的 init() 中通过 Register 注册。
var registry = map[string]Factory{}

// Register 注册一个驱动工厂。重复注册同名驱动会 panic，
// 因为这通常意味着构建配置出错（多个 init 同时 Register）。
func Register(name string, f Factory) {
	if name == "" {
		panic("dbexec: Register called with empty name")
	}
	if f == nil {
		panic("dbexec: Register called with nil factory for " + name)
	}
	if _, exists := registry[name]; exists {
		panic("dbexec: duplicate driver registration: " + name)
	}
	registry[name] = f
}

// Open 根据 Connection.Driver 查表并调用对应的 Factory。
//
// 当 Connection.Driver 未注册时返回 ErrUnsupportedDriver 的包装错误，
// 调用方可以 errors.Is(err, ErrUnsupportedDriver) 识别。
func Open(c *model.Connection) (Driver, error) {
	if c == nil {
		return nil, fmt.Errorf("dbexec: Open called with nil connection")
	}
	f, ok := registry[c.Driver]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDriver, c.Driver)
	}
	return f(c)
}

// DriverInfo 是 "name + capabilities" 的组合，用于对外描述已注册的驱动。
// 字段名与 Capabilities 的 JSON tag 平级拼接，便于直接序列化给前端。
type DriverInfo struct {
	Name string `json:"name"`
	Capabilities
}

// ListDrivers 枚举所有已注册的驱动及其 Capabilities，按 name 字典序返回。
// 排序保证前端可以稳定缓存，也方便测试断言。
//
// 构造一个零值 Connection 只是为了复用 Factory 拿到 Capabilities——
// Capabilities 本身是静态声明，不应该触发任何 I/O。
func ListDrivers() []DriverInfo {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]DriverInfo, 0, len(names))
	for _, name := range names {
		f := registry[name]
		d, err := f(&model.Connection{Driver: name})
		if err != nil || d == nil {
			// Capabilities 声明是静态的，理论上不会失败；万一某个 Factory
			// 将来做了 DSN 预校验，这里传空 Connection 可能出错——
			// 我们选择跳过而不是 panic，保持接口的"只读元信息"语义。
			continue
		}
		out = append(out, DriverInfo{
			Name:         name,
			Capabilities: d.Capabilities(),
		})
	}
	return out
}
