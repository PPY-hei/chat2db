# AI 对话文件上传功能

## 功能概述

在 AI 对话中支持上传文件（图片/Excel/CSV），**自动解析文件内容**并将数据摘要传递给 AI，让 AI 能够直接理解和分析数据。

## 核心特性

✅ **自动解析数据文件**：上传 CSV/Excel 后立即解析，提取列名、行数和前 5 行数据预览  
✅ **数据摘要传递**：将解析后的数据结构和预览直接传给 AI，无需手动描述  
✅ **智能问答**：AI 可以直接回答关于数据的问题，如"有哪些列"、"数据量多大"等  
✅ **SQL 生成**：基于实际数据结构生成准确的 SQL 语句

## 支持的文件类型

- **图片**: `.jpg`, `.jpeg`, `.png`, `.gif`, `.bmp`
- **Excel**: `.xlsx`, `.xls`
- **CSV/文本**: `.csv`, `.txt`

## 文件大小限制

- 最大上传大小: 100MB

## 使用方式

### 1. 上传文件

在 AI 对话框中，点击"上传文件（图片/Excel/CSV）"按钮，选择要上传的文件。

### 2. 查看已上传文件

上传成功后，文件会显示在对话框中，包括：
- 文件图标（根据类型显示）
- 文件名
- 文件大小
- 删除按钮

### 3. 与 AI 对话

上传文件后，**数据会自动解析并传递给 AI**，你可以直接提问：

#### 示例 1: 查看数据结构

```
上传文件: test_data.csv

提示词: 这个文件有哪些列？数据量多大？
```

AI 会看到：
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
3 | 王五 | 25 | 广州 | 12000
4 | 赵六 | 35 | 深圳 | 22000
5 | 钱七 | 29 | 杭州 | 16000
```

AI 回答：这个文件包含 5 列（id, name, age, city, salary），共 10 行数据。

#### 示例 2: 生成建表 SQL

```
上传文件: sales_data.xlsx

提示词: 根据这个文件的数据结构，生成创建表的 SQL 语句
```

AI 会根据实际的列名和数据类型生成准确的 CREATE TABLE 语句。

#### 示例 3: 数据分析查询

```
上传文件: employees.csv

提示词: 查询工资最高的前 5 名员工
```

AI 会基于文件中的实际列名（如 salary, name）生成正确的 SQL。

## 技术实现

### 后端 API

#### 1. 文件上传（带自动解析）
```
POST /api/ai/upload
Content-Type: multipart/form-data

Response:
{
  "file_id": "uuid",
  "filename": "example.csv",
  "size": 12345,
  "path": "/path/to/file",
  "uploaded_at": 1234567890,
  "file_type": ".csv",
  "original_name": "example.csv",
  "data_summary": "CSV 文件数据摘要：\n- 总行数: 100\n- 列数: 5\n..."
}
```

**自动解析逻辑**：
- CSV 文件：使用 Go 标准库 `encoding/csv` 解析
- Excel 文件：使用 `github.com/xuri/excelize/v2` 解析
- 提取：列名、总行数、前 5 行数据预览
- 格式化为易读的文本摘要

#### 2. 脚本执行
```
POST /api/ai/execute-script
Content-Type: application/json

Body:
{
  "script": "import pandas as pd\n...",
  "file_ids": ["uuid1", "uuid2"],
  "language": "python3"
}

Response:
{
  "output": "script output",
  "exit_code": 0,
  "success": true
}
```

#### 3. 删除文件
```
DELETE /api/ai/files/:fileID

Response:
{
  "ok": true
}
```

### 前端集成

文件上传功能已集成到 `SQLTab.tsx` 的 AI 对话框中：

1. **状态管理**: 使用 `uploadedFiles` 状态存储已上传的文件列表（包含 `data_summary`）
2. **上传处理**: `handleFileUpload` 函数处理文件上传，接收后端返回的数据摘要
3. **文件显示**: 在对话框中显示已上传文件的列表
4. **AI 集成**: 在 `askAI` 函数中将 `data_summary` 直接添加到 AI 提示词中

**关键代码**：
```typescript
// 构建文件上下文
let fileContext = "";
if (uploadedFiles.length > 0) {
  fileContext = "\n\n用户上传了以下文件：\n";
  for (const file of uploadedFiles) {
    fileContext += `\n文件名: ${file.filename}\n`;
    if (file.data_summary) {
      fileContext += file.data_summary + "\n";  // 直接包含解析后的数据
    }
  }
}

// 发送给 AI
const resp = await api.aiChat({
  prompt: aiPrompt + fileContext,  // 数据摘要自动附加
  dialect,
  selection,
  table_ddl: combinedDDL || undefined,
});
```

### 文件存储

- 上传的文件存储在 `./data/uploads` 目录
- 文件命名格式: `{userID}_{fileID}{extension}`
- 文件会在用户删除或会话结束后清理

## 安全考虑

1. **文件类型验证**: 只允许特定类型的文件上传
2. **文件大小限制**: 限制最大上传大小为 100MB
3. **用户隔离**: 每个用户只能访问自己上传的文件
4. **脚本沙箱**: Python 脚本在受限环境中执行

## 依赖要求

### Go 依赖

```bash
go get github.com/google/uuid
go get github.com/xuri/excelize/v2
```

### Python 环境（可选）

如果需要使用脚本执行功能：

```bash
pip install pandas openpyxl xlrd
```

## 测试示例

项目提供了测试数据文件 `docs/test_data.csv`，包含 10 行员工数据（id, name, age, city, salary）。

**测试步骤**：
1. 启动应用，打开 SQL 编辑器
2. 点击 AI 按钮打开对话框
3. 上传 `test_data.csv` 文件
4. 提问："这个文件有哪些列？"
5. AI 会立即回答，因为数据已经解析并传递给它了

## 未来改进

1. ~~**自动解析文件内容**~~：✅ 已实现
2. **图片 OCR**：支持从图片中提取表格数据
3. **更大文件支持**：流式解析大文件，避免内存溢出
4. **数据类型推断**：自动推断列的数据类型（INT, VARCHAR, DATE 等）
5. **多表关联**：上传多个文件时自动识别可能的关联关系
6. **数据清洗建议**：AI 分析数据质量并提供清洗建议
