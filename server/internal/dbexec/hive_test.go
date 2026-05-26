package dbexec

import (
	"testing"
)

func TestParseHiveJDBC(t *testing.T) {
	tests := []struct {
		name    string
		jdbc    string
		want    *hiveJDBCConfig
		wantErr bool
	}{
		{
			name: "simple direct connection",
			jdbc: "jdbc:hive2://localhost:10000/default",
			want: &hiveJDBCConfig{
				Hosts:              []string{"localhost:10000"},
				Database:           "default",
				ZooKeeperMode:      false,
				ZooKeeperNamespace: "hiveserver2",
				TransportMode:      "binary",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "zookeeper mode with multiple hosts",
			jdbc: "jdbc:hive2://zk0:2181,zk1:2181,zk2:2181/;serviceDiscoveryMode=zooKeeper;zooKeeperNamespace=hiveserver2",
			want: &hiveJDBCConfig{
				Hosts:              []string{"zk0:2181", "zk1:2181", "zk2:2181"},
				Database:           "default",
				ZooKeeperMode:      true,
				ZooKeeperNamespace: "hiveserver2",
				TransportMode:      "binary",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "http transport with ssl",
			jdbc: "jdbc:hive2://host:443/default;ssl=true?hive.server2.transport.mode=http;hive.server2.thrift.http.path=/hive2",
			want: &hiveJDBCConfig{
				Hosts:              []string{"host:443"},
				Database:           "default",
				ZooKeeperMode:      false,
				ZooKeeperNamespace: "hiveserver2",
				SSL:                true,
				TransportMode:      "http",
				HTTPPath:           "/hive2",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "azure hdinsight style",
			jdbc: "jdbc:hive2://muji-cdp-hdi-cluster.azurehdinsight.cn:443/default;ssl=true?hive.server2.transport.mode=http;hive.server2.thrift.http.path=/hive2",
			want: &hiveJDBCConfig{
				Hosts:              []string{"muji-cdp-hdi-cluster.azurehdinsight.cn:443"},
				Database:           "default",
				ZooKeeperMode:      false,
				ZooKeeperNamespace: "hiveserver2",
				SSL:                true,
				TransportMode:      "http",
				HTTPPath:           "/hive2",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "complex zookeeper with long hosts",
			jdbc: "jdbc:hive2://zk0-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181,zk1-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181,zk2-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181/;serviceDiscoveryMode=zooKeeper;zooKeeperNamespace=hiveserver2",
			want: &hiveJDBCConfig{
				Hosts: []string{
					"zk0-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181",
					"zk1-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181",
					"zk2-muji-c.sj5aafjg3txepf5sb5rmmgwtyb.zqzx.internal.chinacloudapp.cn:2181",
				},
				Database:           "default",
				ZooKeeperMode:      true,
				ZooKeeperNamespace: "hiveserver2",
				TransportMode:      "binary",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "no database specified",
			jdbc: "jdbc:hive2://localhost:10000",
			want: &hiveJDBCConfig{
				Hosts:              []string{"localhost:10000"},
				Database:           "default",
				ZooKeeperMode:      false,
				ZooKeeperNamespace: "hiveserver2",
				TransportMode:      "binary",
				SessionParams:      map[string]string{},
			},
		},
		{
			name: "with custom session params",
			jdbc: "jdbc:hive2://localhost:10000/mydb;customParam=value;anotherParam=123",
			want: &hiveJDBCConfig{
				Hosts:              []string{"localhost:10000"},
				Database:           "mydb",
				ZooKeeperMode:      false,
				ZooKeeperNamespace: "hiveserver2",
				TransportMode:      "binary",
				SessionParams: map[string]string{
					"customParam":   "value",
					"anotherParam":  "123",
				},
			},
		},
		{
			name:    "invalid url - not hive2",
			jdbc:    "jdbc:mysql://localhost:3306/db",
			wantErr: true,
		},
		{
			name:    "invalid url - no host",
			jdbc:    "jdbc:hive2:///default",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHiveJDBC(tt.jdbc)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHiveJDBC() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			// 比较结果
			if len(got.Hosts) != len(tt.want.Hosts) {
				t.Errorf("Hosts length = %d, want %d", len(got.Hosts), len(tt.want.Hosts))
			}
			for i := range got.Hosts {
				if i < len(tt.want.Hosts) && got.Hosts[i] != tt.want.Hosts[i] {
					t.Errorf("Hosts[%d] = %q, want %q", i, got.Hosts[i], tt.want.Hosts[i])
				}
			}
			if got.Database != tt.want.Database {
				t.Errorf("Database = %q, want %q", got.Database, tt.want.Database)
			}
			if got.ZooKeeperMode != tt.want.ZooKeeperMode {
				t.Errorf("ZooKeeperMode = %v, want %v", got.ZooKeeperMode, tt.want.ZooKeeperMode)
			}
			if got.ZooKeeperNamespace != tt.want.ZooKeeperNamespace {
				t.Errorf("ZooKeeperNamespace = %q, want %q", got.ZooKeeperNamespace, tt.want.ZooKeeperNamespace)
			}
			if got.SSL != tt.want.SSL {
				t.Errorf("SSL = %v, want %v", got.SSL, tt.want.SSL)
			}
			if got.TransportMode != tt.want.TransportMode {
				t.Errorf("TransportMode = %q, want %q", got.TransportMode, tt.want.TransportMode)
			}
			if got.HTTPPath != tt.want.HTTPPath {
				t.Errorf("HTTPPath = %q, want %q", got.HTTPPath, tt.want.HTTPPath)
			}
			if len(got.SessionParams) != len(tt.want.SessionParams) {
				t.Errorf("SessionParams length = %d, want %d", len(got.SessionParams), len(tt.want.SessionParams))
			}
			for k, v := range tt.want.SessionParams {
				if got.SessionParams[k] != v {
					t.Errorf("SessionParams[%q] = %q, want %q", k, got.SessionParams[k], v)
				}
			}
		})
	}
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		name     string
		hp       string
		fallback int
		wantHost string
		wantPort int
	}{
		{
			name:     "host with port",
			hp:       "localhost:10000",
			fallback: 9999,
			wantHost: "localhost",
			wantPort: 10000,
		},
		{
			name:     "host without port",
			hp:       "localhost",
			fallback: 9999,
			wantHost: "localhost",
			wantPort: 9999,
		},
		{
			name:     "empty string",
			hp:       "",
			fallback: 9999,
			wantHost: "",
			wantPort: 9999,
		},
		{
			name:     "ipv4 with port",
			hp:       "192.168.1.1:2181",
			fallback: 9999,
			wantHost: "192.168.1.1",
			wantPort: 2181,
		},
		{
			name:     "fqdn with port",
			hp:       "zk0-muji-c.example.com:2181",
			fallback: 9999,
			wantHost: "zk0-muji-c.example.com",
			wantPort: 2181,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHost, gotPort := splitHostPort(tt.hp, tt.fallback)
			if gotHost != tt.wantHost {
				t.Errorf("splitHostPort() host = %q, want %q", gotHost, tt.wantHost)
			}
			if gotPort != tt.wantPort {
				t.Errorf("splitHostPort() port = %d, want %d", gotPort, tt.wantPort)
			}
		})
	}
}

func TestIsHiveJDBC(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{
			name: "valid hive2 jdbc url",
			s:    "jdbc:hive2://localhost:10000/default",
			want: true,
		},
		{
			name: "valid hive2 jdbc url with uppercase",
			s:    "JDBC:HIVE2://localhost:10000/default",
			want: true,
		},
		{
			name: "valid hive2 jdbc url with spaces",
			s:    "  jdbc:hive2://localhost:10000/default  ",
			want: true,
		},
		{
			name: "mysql jdbc url",
			s:    "jdbc:mysql://localhost:3306/db",
			want: false,
		},
		{
			name: "plain host",
			s:    "localhost:10000",
			want: false,
		},
		{
			name: "empty string",
			s:    "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHiveJDBC(tt.s); got != tt.want {
				t.Errorf("isHiveJDBC() = %v, want %v", got, tt.want)
			}
		})
	}
}
