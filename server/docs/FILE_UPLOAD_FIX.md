# 文件上传功能使用说明

## 问题修复

**之前的问题**：上传 CSV/Excel 文件后，AI 无法看到文件内容，只知道文件名。

**现在的解决方案**：上传时自动解析文件，将数据结构和预览传给 AI。

## 现在的工作流程

1. **上传文件** → 后端自动解析
2. **提取数据摘要**：
   - 列名（如：id, name, age, city, salary）
   - 总行数（如：100 行）
   - 前 5 行数据预览
3. **传给 AI** → AI 可以直接看到数据结构和内容
4. **智能回答** → AI 基于实际数据回答问题

## 示例对话

### 上传 test_data.csv 后：

**你问**：这个文件有哪些列？

**AI 看到的上下文**：
```
CSV 文件数据摘要：
- 总行数: 10
- 列数: 5
- 列名: id, name, age, city, salary

前 5 行数据预览：
id | name | age | city | salary
--------------------------------
1 | 张三 | 28 | 北京 | 15000
2 | 李四 | 32 | 上海 | 18000
...
```

**AI 回答**：这个文件包含 5 列：id（编号）、name（姓名）、age（年龄）、city（城市）、salary（工资）。共有 10 行数据。

---

**你问**：生成一个查询，找出工资最高的 3 个人

**AI 生成**：
```sql
SELECT name, city, salary
FROM employees
ORDER BY salary DESC
LIMIT 3;
```

## 技术细节

- **CSV 解析**：Go 标准库 `encoding/csv`
- **Excel 解析**：`github.com/xuri/excelize/v2`
- **数据传递**：通过 `data_summary` 字段自动附加到 AI prompt
- **无需额外配置**：开箱即用

## 测试

使用项目提供的 `docs/test_data.csv` 测试：
```bash
# 启动应用
cd server && go run cmd/server/main.go

# 打开浏览器，上传 test_data.csv
# 提问："这个文件有多少行？有哪些列？"
```

AI 会立即给出准确答案，因为数据已经解析并传递给它了。
