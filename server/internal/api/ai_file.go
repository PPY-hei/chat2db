package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chy/chat2db/server/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

const (
	maxUploadSize = 100 * 1024 * 1024 // 100MB
	uploadDir     = "./data/uploads"
)

func init() {
	// 确保上传目录存在
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		panic(fmt.Sprintf("failed to create upload directory: %v", err))
	}
}

// UploadFile 处理文件上传请求
func UploadFile(c *gin.Context) {
	uid := middleware.CurrentUserID(c)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		badRequest(c, fmt.Errorf("failed to get file: %w", err))
		return
	}
	defer file.Close()

	// 验证文件大小
	if header.Size > maxUploadSize {
		badRequest(c, fmt.Errorf("file too large: max %d bytes", maxUploadSize))
		return
	}

	// 验证文件类型
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
		".xlsx": true, ".xls": true, ".csv": true, ".txt": true,
	}
	if !allowedExts[ext] {
		badRequest(c, fmt.Errorf("unsupported file type: %s", ext))
		return
	}

	// 生成唯一文件名
	fileID := uuid.New().String()
	filename := fmt.Sprintf("%d_%s%s", uid, fileID, ext)
	filePath := filepath.Join(uploadDir, filename)

	// 保存文件
	dst, err := os.Create(filePath)
	if err != nil {
		internal(c, fmt.Errorf("failed to create file: %w", err))
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		internal(c, fmt.Errorf("failed to save file: %w", err))
		return
	}

	// 解析文件内容（CSV/Excel）
	var dataSummary string
	if ext == ".csv" {
		dataSummary, _ = parseCSV(filePath)
	} else if ext == ".xlsx" || ext == ".xls" {
		dataSummary, _ = parseExcel(filePath)
	}

	c.JSON(http.StatusOK, gin.H{
		"file_id":       fileID,
		"filename":      header.Filename,
		"size":          header.Size,
		"path":          filePath,
		"uploaded_at":   time.Now().Unix(),
		"file_type":     ext,
		"original_name": header.Filename,
		"data_summary":  dataSummary,
	})
}

type executeScriptReq struct {
	Script   string   `json:"script"`
	FileIDs  []string `json:"file_ids"`
	Language string   `json:"language"` // python, node, etc.
}

// ExecuteScript 执行用户提供的脚本来处理上传的文件
func ExecuteScript(c *gin.Context) {
	uid := middleware.CurrentUserID(c)

	var in executeScriptReq
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, err)
		return
	}

	// 验证语言
	if in.Language != "python" && in.Language != "python3" {
		badRequest(c, fmt.Errorf("unsupported language: %s (only python/python3 supported)", in.Language))
		return
	}

	// 验证脚本不为空
	if strings.TrimSpace(in.Script) == "" {
		badRequest(c, fmt.Errorf("script cannot be empty"))
		return
	}

	// 解析文件路径
	filePaths := make([]string, 0, len(in.FileIDs))
	for _, fileID := range in.FileIDs {
		// 查找匹配的文件
		pattern := filepath.Join(uploadDir, fmt.Sprintf("%d_%s*", uid, fileID))
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			badRequest(c, fmt.Errorf("file not found: %s", fileID))
			return
		}
		filePaths = append(filePaths, matches[0])
	}

	// 创建临时脚本文件
	scriptID := uuid.New().String()
	scriptPath := filepath.Join(uploadDir, fmt.Sprintf("%d_%s.py", uid, scriptID))
	if err := os.WriteFile(scriptPath, []byte(in.Script), 0644); err != nil {
		internal(c, fmt.Errorf("failed to create script file: %w", err))
		return
	}
	defer os.Remove(scriptPath)

	// 设置环境变量传递文件路径
	env := os.Environ()
	if len(filePaths) > 0 {
		env = append(env, fmt.Sprintf("FILE_PATHS=%s", strings.Join(filePaths, ",")))
		env = append(env, fmt.Sprintf("FILE_PATH=%s", filePaths[0])) // 单文件兼容
	}

	// 执行脚本
	cmd := exec.CommandContext(c.Request.Context(), "python3", scriptPath)
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			internal(c, fmt.Errorf("failed to execute script: %w", err))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"output":    string(output),
		"exit_code": exitCode,
		"success":   exitCode == 0,
	})
}

// DeleteUploadedFile 删除上传的文件
func DeleteUploadedFile(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	fileID := c.Param("fileID")

	if fileID == "" {
		badRequest(c, fmt.Errorf("file_id is required"))
		return
	}

	// 查找匹配的文件
	pattern := filepath.Join(uploadDir, fmt.Sprintf("%d_%s*", uid, fileID))
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		badRequest(c, fmt.Errorf("file not found: %s", fileID))
		return
	}

	// 删除文件
	if err := os.Remove(matches[0]); err != nil {
		internal(c, fmt.Errorf("failed to delete file: %w", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// parseCSV 解析 CSV 文件，返回数据摘要（列名、行数、统计信息、数据预览）
func parseCSV(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// 读取表头
	headers, err := reader.Read()
	if err != nil {
		return "", err
	}

	// 读取所有数据用于统计
	var allRows [][]string
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		allRows = append(allRows, row)
	}

	totalRows := len(allRows)
	if totalRows == 0 {
		return "CSV 文件为空", nil
	}

	// 计算每列的统计信息
	colStats := make([]map[string]interface{}, len(headers))
	for i := range headers {
		colStats[i] = analyzeColumn(allRows, i)
	}

	// 构建摘要
	var summary strings.Builder
	summary.WriteString("CSV 文件数据摘要：\n")
	summary.WriteString(fmt.Sprintf("- 总行数: %d\n", totalRows))
	summary.WriteString(fmt.Sprintf("- 列数: %d\n", len(headers)))
	summary.WriteString(fmt.Sprintf("- 列名: %s\n\n", strings.Join(headers, ", ")))

	// 添加每列的统计信息
	summary.WriteString("列统计信息：\n")
	for i, header := range headers {
		stats := colStats[i]
		summary.WriteString(fmt.Sprintf("\n%s:\n", header))
		summary.WriteString(fmt.Sprintf("  - 非空值: %d\n", stats["non_null"]))
		summary.WriteString(fmt.Sprintf("  - 空值: %d\n", stats["null_count"]))
		summary.WriteString(fmt.Sprintf("  - 唯一值数量: %d\n", stats["unique_count"]))

		if stats["is_numeric"].(bool) {
			summary.WriteString(fmt.Sprintf("  - 数据类型: 数值型\n"))
			summary.WriteString(fmt.Sprintf("  - 最小值: %v\n", stats["min"]))
			summary.WriteString(fmt.Sprintf("  - 最大值: %v\n", stats["max"]))
			summary.WriteString(fmt.Sprintf("  - 平均值: %.2f\n", stats["avg"]))
		} else {
			summary.WriteString(fmt.Sprintf("  - 数据类型: 文本型\n"))
			if topValues, ok := stats["top_values"].([]string); ok && len(topValues) > 0 {
				summary.WriteString(fmt.Sprintf("  - 最常见的值: %s\n", strings.Join(topValues, ", ")))
			}
		}
	}

	// 数据预览：前 10 行 + 中间 5 行 + 最后 5 行
	summary.WriteString("\n数据预览：\n")
	summary.WriteString(strings.Join(headers, " | ") + "\n")
	summary.WriteString(strings.Repeat("-", len(strings.Join(headers, " | "))) + "\n")

	// 前 10 行
	previewCount := 10
	if totalRows < previewCount {
		previewCount = totalRows
	}
	for i := 0; i < previewCount; i++ {
		summary.WriteString(strings.Join(allRows[i], " | ") + "\n")
	}

	// 如果数据量大，显示中间和末尾的采样
	if totalRows > 20 {
		summary.WriteString("...\n")
		// 中间 5 行
		midStart := totalRows/2 - 2
		for i := midStart; i < midStart+5 && i < totalRows; i++ {
			summary.WriteString(strings.Join(allRows[i], " | ") + "\n")
		}
		summary.WriteString("...\n")
		// 最后 5 行
		for i := totalRows - 5; i < totalRows; i++ {
			if i >= 0 {
				summary.WriteString(strings.Join(allRows[i], " | ") + "\n")
			}
		}
	}

	return summary.String(), nil
}

// analyzeColumn 分析单列数据，返回统计信息
func analyzeColumn(rows [][]string, colIndex int) map[string]interface{} {
	stats := make(map[string]interface{})

	var values []string
	var numericValues []float64
	nullCount := 0
	uniqueValues := make(map[string]int)
	isNumeric := true

	for _, row := range rows {
		if colIndex >= len(row) {
			nullCount++
			continue
		}

		val := strings.TrimSpace(row[colIndex])
		if val == "" {
			nullCount++
			continue
		}

		values = append(values, val)
		uniqueValues[val]++

		// 尝试解析为数值
		if isNumeric {
			if num, err := parseNumber(val); err == nil {
				numericValues = append(numericValues, num)
			} else {
				isNumeric = false
			}
		}
	}

	stats["non_null"] = len(values)
	stats["null_count"] = nullCount
	stats["unique_count"] = len(uniqueValues)
	stats["is_numeric"] = isNumeric && len(numericValues) > 0

	if stats["is_numeric"].(bool) {
		// 数值型统计
		min, max, avg := calculateNumericStats(numericValues)
		stats["min"] = min
		stats["max"] = max
		stats["avg"] = avg
	} else {
		// 文本型统计：找出最常见的值（前 3 个）
		topValues := getTopValues(uniqueValues, 3)
		stats["top_values"] = topValues
	}

	return stats
}

// parseNumber 尝试将字符串解析为数值
func parseNumber(s string) (float64, error) {
	// 移除常见的数值格式字符
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")

	var num float64
	_, err := fmt.Sscanf(s, "%f", &num)
	return num, err
}

// calculateNumericStats 计算数值统计信息
func calculateNumericStats(values []float64) (min, max, avg float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}

	min = values[0]
	max = values[0]
	sum := 0.0

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}

	avg = sum / float64(len(values))
	return
}

// getTopValues 获取出现频率最高的值
func getTopValues(valueCount map[string]int, topN int) []string {
	type kv struct {
		Key   string
		Value int
	}

	var sorted []kv
	for k, v := range valueCount {
		sorted = append(sorted, kv{k, v})
	}

	// 简单排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Value > sorted[i].Value {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var result []string
	for i := 0; i < topN && i < len(sorted); i++ {
		result = append(result, sorted[i].Key)
	}

	return result
}

// parseExcel 解析 Excel 文件，返回数据摘要
func parseExcel(filePath string) (string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// 获取第一个工作表
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", fmt.Errorf("no sheets found")
	}
	sheetName := sheets[0]

	// 读取所有行
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return "", err
	}

	if len(rows) == 0 {
		return "", fmt.Errorf("empty sheet")
	}

	// 表头
	headers := rows[0]
	dataRows := rows[1:]
	totalRows := len(dataRows)

	if totalRows == 0 {
		return "Excel 文件只有表头，没有数据", nil
	}

	// 计算每列的统计信息
	colStats := make([]map[string]interface{}, len(headers))
	for i := range headers {
		colStats[i] = analyzeColumn(dataRows, i)
	}

	// 构建摘要
	var summary strings.Builder
	summary.WriteString("Excel 文件数据摘要：\n")
	summary.WriteString(fmt.Sprintf("- 工作表: %s\n", sheetName))
	summary.WriteString(fmt.Sprintf("- 总行数: %d\n", totalRows))
	summary.WriteString(fmt.Sprintf("- 列数: %d\n", len(headers)))
	summary.WriteString(fmt.Sprintf("- 列名: %s\n\n", strings.Join(headers, ", ")))

	// 添加每列的统计信息
	summary.WriteString("列统计信息：\n")
	for i, header := range headers {
		stats := colStats[i]
		summary.WriteString(fmt.Sprintf("\n%s:\n", header))
		summary.WriteString(fmt.Sprintf("  - 非空值: %d\n", stats["non_null"]))
		summary.WriteString(fmt.Sprintf("  - 空值: %d\n", stats["null_count"]))
		summary.WriteString(fmt.Sprintf("  - 唯一值数量: %d\n", stats["unique_count"]))

		if stats["is_numeric"].(bool) {
			summary.WriteString(fmt.Sprintf("  - 数据类型: 数值型\n"))
			summary.WriteString(fmt.Sprintf("  - 最小值: %v\n", stats["min"]))
			summary.WriteString(fmt.Sprintf("  - 最大值: %v\n", stats["max"]))
			summary.WriteString(fmt.Sprintf("  - 平均值: %.2f\n", stats["avg"]))
		} else {
			summary.WriteString(fmt.Sprintf("  - 数据类型: 文本型\n"))
			if topValues, ok := stats["top_values"].([]string); ok && len(topValues) > 0 {
				summary.WriteString(fmt.Sprintf("  - 最常见的值: %s\n", strings.Join(topValues, ", ")))
			}
		}
	}

	// 数据预览
	summary.WriteString("\n数据预览：\n")
	summary.WriteString(strings.Join(headers, " | ") + "\n")
	summary.WriteString(strings.Repeat("-", len(strings.Join(headers, " | "))) + "\n")

	// 前 10 行
	previewCount := 10
	if totalRows < previewCount {
		previewCount = totalRows
	}
	for i := 0; i < previewCount; i++ {
		row := dataRows[i]
		// 补齐列数
		for len(row) < len(headers) {
			row = append(row, "")
		}
		summary.WriteString(strings.Join(row[:len(headers)], " | ") + "\n")
	}

	// 如果数据量大，显示中间和末尾的采样
	if totalRows > 20 {
		summary.WriteString("...\n")
		// 中间 5 行
		midStart := totalRows/2 - 2
		for i := midStart; i < midStart+5 && i < totalRows; i++ {
			row := dataRows[i]
			for len(row) < len(headers) {
				row = append(row, "")
			}
			summary.WriteString(strings.Join(row[:len(headers)], " | ") + "\n")
		}
		summary.WriteString("...\n")
		// 最后 5 行
		for i := totalRows - 5; i < totalRows; i++ {
			if i >= 0 {
				row := dataRows[i]
				for len(row) < len(headers) {
					row = append(row, "")
				}
				summary.WriteString(strings.Join(row[:len(headers)], " | ") + "\n")
			}
		}
	}

	return summary.String(), nil
}
