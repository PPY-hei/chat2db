package dbexec

import (
	"context"
	"errors"
	"testing"

	"github.com/chy/chat2db/server/internal/model"
)

// stubDriver 是测试用的最小 Driver 实现，只用于覆盖 registry 行为。
type stubDriver struct {
	conn *model.Connection
}

func (s *stubDriver) Name() string             { return "stub" }
func (s *stubDriver) Capabilities() Capabilities { return Capabilities{} }
func (s *stubDriver) WithDatabase(_ string) Driver {
	return s
}
func (s *stubDriver) Ping(_ context.Context) error { return nil }
func (s *stubDriver) Exec(_ context.Context, _ string, _ ...any) (*QueryResult, error) {
	return &QueryResult{}, nil
}
func (s *stubDriver) ListDatabases(_ context.Context) ([]DatabaseInfo, error) { return nil, nil }
func (s *stubDriver) ListSchemas(_ context.Context) ([]Schema, error)         { return nil, nil }
func (s *stubDriver) ListTables(_ context.Context, _ string) ([]TableInfo, error) {
	return nil, nil
}
func (s *stubDriver) ListColumns(_ context.Context, _, _ string) ([]ColumnInfo, error) {
	return nil, nil
}
func (s *stubDriver) GenerateTableDDL(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (s *stubDriver) Invalidate() {}

// withTempRegistry 临时替换全局 registry，并在测试结束后恢复，避免并发副作用。
func withTempRegistry(t *testing.T, fn func()) {
	t.Helper()
	saved := registry
	registry = map[string]Factory{}
	t.Cleanup(func() { registry = saved })
	fn()
}

func TestOpen_UnknownDriverReturnsSentinel(t *testing.T) {
	withTempRegistry(t, func() {
		_, err := Open(&model.Connection{Driver: "no-such-driver"})
		if err == nil {
			t.Fatalf("expected error for unknown driver, got nil")
		}
		if !errors.Is(err, ErrUnsupportedDriver) {
			t.Fatalf("expected errors.Is(err, ErrUnsupportedDriver) to be true, got %v", err)
		}
	})
}

func TestOpen_NilConnection(t *testing.T) {
	withTempRegistry(t, func() {
		_, err := Open(nil)
		if err == nil {
			t.Fatalf("expected error for nil connection, got nil")
		}
	})
}

func TestRegisterAndOpen_RoundTrip(t *testing.T) {
	withTempRegistry(t, func() {
		Register("stub", func(c *model.Connection) (Driver, error) {
			return &stubDriver{conn: c}, nil
		})

		c := &model.Connection{Driver: "stub"}
		d, err := Open(c)
		if err != nil {
			t.Fatalf("Open returned unexpected error: %v", err)
		}
		if d.Name() != "stub" {
			t.Fatalf("Driver.Name() = %q, want stub", d.Name())
		}
	})
}

func TestRegister_PanicsOnEmptyName(t *testing.T) {
	withTempRegistry(t, func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("expected panic for empty name, got nil")
			}
		}()
		Register("", func(_ *model.Connection) (Driver, error) { return nil, nil })
	})
}

func TestRegister_PanicsOnNilFactory(t *testing.T) {
	withTempRegistry(t, func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("expected panic for nil factory, got nil")
			}
		}()
		Register("stub", nil)
	})
}

func TestRegister_PanicsOnDuplicate(t *testing.T) {
	withTempRegistry(t, func() {
		factory := func(_ *model.Connection) (Driver, error) { return nil, nil }
		Register("stub", factory)
		defer func() {
			if recover() == nil {
				t.Fatalf("expected panic for duplicate registration, got nil")
			}
		}()
		Register("stub", factory)
	})
}

// TestBuiltinDriversRegistered 锁定 init() 注册了 postgres 与 mysql。
// 这是 PR #3（移除 meta.go 中 switch）的安全网。
func TestBuiltinDriversRegistered(t *testing.T) {
	for _, name := range []string{"postgres", "mysql"} {
		if _, ok := registry[name]; !ok {
			t.Errorf("expected built-in driver %q to be registered", name)
		}
	}
}
