package dbexec

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/beltran/gohive"
	"github.com/chy/chat2db/server/internal/config"
	"github.com/chy/chat2db/server/internal/model"
)

// tlsConfigInsecure 是一个跳过证书验证的 TLS 配置，用于 Hive SSL 连接。
var tlsConfigInsecure = tls.Config{InsecureSkipVerify: true}

// hivePoolKey uniquely identifies a Hive connection pool by connID + database.
type hivePoolKey struct {
	connID   uint
	database string
}

type hivePoolEntry struct {
	conn    *gohive.Connection
	version string
}

var (
	hivePoolMu sync.Mutex
	hivePools  = map[hivePoolKey]*hivePoolEntry{}
)

// hiveJDBCConfig 表示从 Hive JDBC URL 解析出的连接配置。
//
// 兼容 JDBC 格式如：
//
//	jdbc:hive2://host1:port1,host2:port2/database;serviceDiscoveryMode=zooKeeper;...
//	jdbc:hive2://host:10000/default
//	jdbc:hive2://host:443/default;ssl=true?hive.server2.transport.mode=http;hive.server2.thrift.http.path=/hive2
type hiveJDBCConfig struct {
	// Hosts 是直连模式下的主机列表（host:port）；ZooKeeper 模式下是 ZK 节点列表。
	Hosts []string
	// Database 是默认数据库（Path 部分）。
	Database string
	// ZooKeeperMode 表示是否为 ZK 服务发现模式。
	ZooKeeperMode bool
	// ZooKeeperNamespace 是 ZK 模式下的命名空间，默认 "hiveserver2"。
	ZooKeeperNamespace string
	// SSL 表示是否启用 TLS。
	SSL bool
	// TransportMode 是传输模式："binary"（默认）或 "http"。
	TransportMode string
	// HTTPPath 是 HTTP 模式下的服务端路径，例如 "/hive2"。
	HTTPPath string
	// SessionParams 是其他 ;k=v 形式的会话参数。
	SessionParams map[string]string
}

// parseHiveJDBC 解析 Hive JDBC URL。
// 容错地处理 ; 与 ? 两种参数分隔风格。
func parseHiveJDBC(jdbc string) (*hiveJDBCConfig, error) {
	s := strings.TrimSpace(jdbc)
	if !strings.HasPrefix(strings.ToLower(s), "jdbc:hive2://") {
		return nil, fmt.Errorf("not a hive JDBC URL: %q", jdbc)
	}
	s = s[len("jdbc:hive2://"):]

	cfg := &hiveJDBCConfig{
		ZooKeeperNamespace: "hiveserver2",
		TransportMode:      "binary",
		SessionParams:      map[string]string{},
	}

	// 把 ?xxx 部分（HiveConf 配置）合并到 ; 段中统一处理
	if idx := strings.Index(s, "?"); idx >= 0 {
		head, tail := s[:idx], s[idx+1:]
		if !strings.HasSuffix(head, ";") && tail != "" {
			head += ";"
		}
		s = head + tail
	}

	// 分离 host[,host...]/path 与 ;params 部分
	var hostsAndPath, paramsPart string
	if idx := strings.Index(s, ";"); idx >= 0 {
		hostsAndPath = s[:idx]
		paramsPart = s[idx+1:]
	} else {
		hostsAndPath = s
	}

	// 拆分出 hosts 与 path
	hostsStr := hostsAndPath
	pathStr := ""
	if idx := strings.Index(hostsAndPath, "/"); idx >= 0 {
		hostsStr = hostsAndPath[:idx]
		pathStr = hostsAndPath[idx+1:]
	}
	if hostsStr == "" {
		return nil, fmt.Errorf("hive JDBC URL missing host: %q", jdbc)
	}
	for _, h := range strings.Split(hostsStr, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			cfg.Hosts = append(cfg.Hosts, h)
		}
	}
	cfg.Database = strings.TrimSpace(pathStr)

	// 解析 ;k=v;k=v 参数（kv 大小写不敏感的常见键）
	for _, kv := range strings.Split(paramsPart, ";") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(kv[:eq])
		v := strings.TrimSpace(kv[eq+1:])
		switch strings.ToLower(k) {
		case "servicediscoverymode":
			if strings.EqualFold(v, "zookeeper") {
				cfg.ZooKeeperMode = true
			}
		case "zookeepernamespace":
			if v != "" {
				cfg.ZooKeeperNamespace = v
			}
		case "ssl":
			cfg.SSL = strings.EqualFold(v, "true") || v == "1"
		case "hive.server2.transport.mode":
			cfg.TransportMode = strings.ToLower(v)
		case "hive.server2.thrift.http.path", "httppath":
			cfg.HTTPPath = v
		default:
			cfg.SessionParams[k] = v
		}
	}

	if cfg.Database == "" {
		cfg.Database = "default"
	}
	return cfg, nil
}

// splitHostPort 拆 "host:port"，缺省端口由 fallback 提供。
func splitHostPort(hp string, fallback int) (string, int) {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return "", fallback
	}
	if idx := strings.LastIndexByte(hp, ':'); idx >= 0 {
		host := hp[:idx]
		var port int
		if _, err := fmt.Sscanf(hp[idx+1:], "%d", &port); err == nil && port > 0 {
			return host, port
		}
		return host, fallback
	}
	return hp, fallback
}

// isHiveJDBC 判断 host 字段是否为 JDBC URL 形式。
// 用户可能把整段 jdbc:hive2://... 填到 host 字段（如 DBeaver 风格）。
func isHiveJDBC(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "jdbc:hive2://")
}

// hiveConnectionConfig builds a gohive connection configuration from model.Connection.
func hiveConnectionConfig(c *model.Connection) (host string, port int, auth string, cfg *gohive.ConnectConfiguration, err error) {
	pwd, err := decryptConnPassword(c)
	if err != nil {
		return "", 0, "", nil, err
	}

	// 优先尝试把 Host 字段当作 JDBC URL 解析
	var jdbc *hiveJDBCConfig
	if isHiveJDBC(c.Host) {
		jdbc, err = parseHiveJDBC(c.Host)
		if err != nil {
			return "", 0, "", nil, err
		}
	}

	cfg = gohive.NewConnectConfiguration()
	cfg.Service = "hive"

	// 设置认证方式
	// HTTP 模式下：NONE（基本认证）或 KERBEROS
	// Binary 模式下：NOSASL, NONE, PLAIN, KERBEROS, DIGEST-MD5
	auth = "NONE"
	if c.Username != "" {
		cfg.Username = c.Username
		cfg.Password = pwd
		// HTTP 模式下使用 NONE（会通过 URL 传递用户名密码）
		// Binary 模式下使用 PLAIN（会通过 SASL 传递）
		auth = "NONE"
	}

	// 数据库名称：JDBC URL > Connection.Database > "default"
	switch {
	case jdbc != nil && jdbc.Database != "":
		cfg.Database = jdbc.Database
	case c.Database != "":
		cfg.Database = c.Database
	default:
		cfg.Database = "default"
	}

	// 连接超时
	cfg.ConnectTimeout = 10 * time.Second
	cfg.SocketTimeout = 30 * time.Second
	cfg.HttpTimeout = 30 * time.Second

	// 应用 JDBC URL 的传输/SSL 配置
	if jdbc != nil {
		if jdbc.TransportMode == "http" {
			cfg.TransportMode = "http"
			cfg.HTTPPath = strings.TrimPrefix(jdbc.HTTPPath, "/")
			if cfg.HTTPPath == "" {
				cfg.HTTPPath = "cliservice"
			}
		}
		if jdbc.SSL {
			cfg.TLSConfig = &tlsConfigInsecure
		}
		// 把会话参数透传
		if cfg.HiveConfiguration == nil {
			cfg.HiveConfiguration = map[string]string{}
		}
		for k, v := range jdbc.SessionParams {
			cfg.HiveConfiguration[k] = v
		}
		if jdbc.ZooKeeperMode {
			cfg.ZookeeperNamespace = jdbc.ZooKeeperNamespace
		}
	}

	// 也允许用户通过 SSLMode 字段独立启用 TLS
	if c.SSLMode != "" && c.SSLMode != "disable" && cfg.TLSConfig == nil {
		cfg.TLSConfig = &tlsConfigInsecure
	}

	// 解析最终的 host 与 port，并选择直连或 ZK 模式
	switch {
	case jdbc != nil && jdbc.ZooKeeperMode:
		// ZK 模式：返回逗号分隔的 ZK 节点列表，port 取首个节点
		host = strings.Join(jdbc.Hosts, ",")
		_, port = splitHostPort(jdbc.Hosts[0], 2181)
	case jdbc != nil:
		// 直连模式：取第一个 host 作为目标
		defaultPort := 10000
		if jdbc.SSL && jdbc.TransportMode == "http" {
			defaultPort = 443
		}
		host, port = splitHostPort(jdbc.Hosts[0], defaultPort)
		// SSH 隧道改写
		if c.SSHEnabled {
			host, port, err = getSSHTunnel(c)
			if err != nil {
				return "", 0, "", nil, err
			}
		}
	default:
		// 经典模式：直接使用 Host/Port 字段
		host, port, err = getSSHTunnel(c)
		if err != nil {
			return "", 0, "", nil, err
		}
	}

	return host, port, auth, cfg, nil
}

// hiveGetPool returns a cached *gohive.Connection for the Hive connection.
// 注意：由于 gohive HTTP 客户端在某些情况下会出现 nil pointer 问题，
// 我们暂时禁用连接池，每次都创建新连接以确保稳定性。
func hiveGetPool(ctx context.Context, c *model.Connection) (*gohive.Connection, error) {
	// 暂时禁用连接池，直接创建新连接
	// TODO: 等 gohive 修复 HTTP 客户端的 nil pointer 问题后再启用连接池

	host, port, auth, cfg, err := hiveConnectionConfig(c)
	if err != nil {
		return nil, err
	}

	var conn *gohive.Connection
	// 判断是否为 ZooKeeper 模式（host 包含逗号分隔的多个节点）
	if strings.Contains(host, ",") {
		// ZooKeeper 服务发现模式
		conn, err = gohive.ConnectZookeeper(host, auth, cfg)
	} else {
		// 直连模式
		conn, err = gohive.Connect(host, port, auth, cfg)
	}
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// hivePing tests the Hive connection without caching.
func hivePing(ctx context.Context, c *model.Connection) error {
	host, port, auth, cfg, err := hiveConnectionConfig(c)
	if err != nil {
		return err
	}

	var conn *gohive.Connection
	if strings.Contains(host, ",") {
		conn, err = gohive.ConnectZookeeper(host, auth, cfg)
	} else {
		conn, err = gohive.Connect(host, port, auth, cfg)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	// 尝试执行一个简单的查询来验证连接
	cursor := conn.Cursor()
	defer cursor.Close()
	cursor.Exec(ctx, "SELECT 1")
	if cursor.Err != nil {
		return cursor.Err
	}
	return nil
}

// hiveExec runs one SQL statement and returns a result.
func hiveExec(ctx context.Context, c *model.Connection, sqlStr string, args ...any) (*QueryResult, error) {
	appCfg := config.Get()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(appCfg.QueryTimeoutSec)*time.Second)
	defer cancel()

	conn, err := hiveGetPool(ctx, c)
	if err != nil {
		return nil, err
	}
	// 由于禁用了连接池，每次使用后都要关闭连接
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	start := time.Now()

	cursor := conn.Cursor()
	defer func() {
		// 防御性关闭，捕获可能的 panic
		if r := recover(); r != nil {
			// 忽略 Close 时的 panic
		}
		cursor.Close()
	}()

	// 执行SQL
	cursor.Exec(ctx, sqlStr)
	if cursor.Err != nil {
		return nil, cursor.Err
	}

	out := &QueryResult{}

	// 判断是否为返回结果集的语句
	if !hiveIsQuery(sqlStr) {
		// 非查询语句，返回成功状态
		out.RowsAffected = 0
		out.ElapsedMs = time.Since(start).Milliseconds()
		out.Tag = "OK"
		return out, nil
	}

	// 查询语句，获取结果集
	desc := cursor.Description()
	if desc != nil && len(desc) > 0 {
		cols := make([]string, len(desc))
		types := make([]string, len(desc))
		for i, colDesc := range desc {
			if len(colDesc) > 0 {
				cols[i] = colDesc[0]
			}
			if len(colDesc) > 1 {
				types[i] = colDesc[1]
			}
		}
		out.Columns = cols
		out.Types = types

		limit := appCfg.QueryMaxRows
		count := 0
		for cursor.HasMore(ctx) {
			if count >= limit {
				out.Truncated = true
				break
			}

			// 使用 RowMap 获取数据，返回 map[string]interface{}
			rowMap := cursor.RowMap(ctx)
			if cursor.Err != nil {
				return nil, cursor.Err
			}
			if rowMap == nil {
				break
			}

			// 按列顺序构建行数据
			row := make([]any, len(cols))
			for i, colName := range cols {
				if val, ok := rowMap[colName]; ok {
					row[i] = val
				}
			}
			row = normalizeRowValuesForJSON(out.Types, row)

			out.Rows = append(out.Rows, row)
			count++
		}
		out.RowsAffected = int64(len(out.Rows))
	}

	out.ElapsedMs = time.Since(start).Milliseconds()
	out.Tag = "OK"
	return out, nil
}

// hiveIsQuery 判断 SQL 是否为返回结果集的查询语句。
func hiveIsQuery(sqlStr string) bool {
	s := strings.TrimSpace(sqlStr)
	// 跳过前导注释
	for {
		if strings.HasPrefix(s, "--") {
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+1:])
			continue
		}
		if strings.HasPrefix(s, "/*") {
			idx := strings.Index(s, "*/")
			if idx < 0 {
				return false
			}
			s = strings.TrimSpace(s[idx+2:])
			continue
		}
		break
	}
	upper := strings.ToUpper(s)
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC ") ||
		strings.HasPrefix(upper, "EXPLAIN") ||
		strings.HasPrefix(upper, "WITH")
}

// hiveInvalidatePool closes and removes all cached Hive pools for a connection.
func hiveInvalidatePool(connID uint) {
	hivePoolMu.Lock()
	for key, e := range hivePools {
		if key.connID == connID {
			e.conn.Close()
			delete(hivePools, key)
		}
	}
	hivePoolMu.Unlock()
}
