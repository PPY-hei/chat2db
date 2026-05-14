package db

import "gorm.io/gorm"

// SetMetaForTest 仅供 *_test.go 使用：替换全局元数据 DB 句柄。
// 传入 nil 表示清空。生产代码不应调用本函数。
func SetMetaForTest(g *gorm.DB) { metaDB = g }
