package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	exportFormatCSV                     = "csv"
	exportFormatInsertSQL               = "insert_sql"
	exportReplacementOnMissingKeep      = "keep"
	exportReplacementOnMissingEmpty     = "empty"
	maxExportWhereLength                = 200000
	maxExportValueReplacementRuleCount  = 50
	maxExportValueReplacementEntryCount = 100000
)

type exportTaskOptions struct {
	Format              string                   `json:"format,omitempty"`
	Where               string                   `json:"where,omitempty"`
	OnConflictDoNothing bool                     `json:"on_conflict_do_nothing,omitempty"`
	ValueReplacements   []ExportValueReplacement `json:"value_replacements,omitempty"`
}

type ExportValueReplacement struct {
	Column    string            `json:"column"`
	Mapping   map[string]string `json:"mapping"`
	OnMissing string            `json:"on_missing,omitempty"`
}

type dataSyncTaskOptions struct {
	Where             string                   `json:"where,omitempty"`
	ValueReplacements []ExportValueReplacement `json:"value_replacements,omitempty"`
}

func buildExportTaskParams(format, where string, onConflictDoNothing bool, valueReplacements []ExportValueReplacement) (string, error) {
	opts, err := normalizeExportTaskOptions(exportTaskOptions{
		Format:              format,
		Where:               where,
		OnConflictDoNothing: onConflictDoNothing,
		ValueReplacements:   valueReplacements,
	})
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return "", fmt.Errorf("marshal export params: %w", err)
	}
	return string(b), nil
}

func buildDataSyncTaskParams(where string, valueReplacements []ExportValueReplacement) (string, error) {
	opts, err := normalizeDataSyncTaskOptions(dataSyncTaskOptions{
		Where:             where,
		ValueReplacements: valueReplacements,
	})
	if err != nil {
		return "", err
	}
	if opts.Where == "" && len(opts.ValueReplacements) == 0 {
		return "", nil
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return "", fmt.Errorf("marshal data sync params: %w", err)
	}
	return string(b), nil
}

func parseExportTaskOptions(raw string) (exportTaskOptions, error) {
	if strings.TrimSpace(raw) == "" {
		return exportTaskOptions{Format: exportFormatCSV}, nil
	}
	var opts exportTaskOptions
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return exportTaskOptions{}, fmt.Errorf("parse export params: %w", err)
	}
	return normalizeExportTaskOptions(opts)
}

func parseDataSyncTaskOptions(raw string) (dataSyncTaskOptions, error) {
	if strings.TrimSpace(raw) == "" {
		return dataSyncTaskOptions{}, nil
	}
	var opts dataSyncTaskOptions
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return dataSyncTaskOptions{}, fmt.Errorf("parse data sync params: %w", err)
	}
	return normalizeDataSyncTaskOptions(opts)
}

func normalizeExportTaskOptions(opts exportTaskOptions) (exportTaskOptions, error) {
	opts.Format = strings.TrimSpace(opts.Format)
	if opts.Format == "" {
		opts.Format = exportFormatCSV
	}
	switch opts.Format {
	case exportFormatCSV, exportFormatInsertSQL:
	default:
		return exportTaskOptions{}, fmt.Errorf("unsupported export format: %s", opts.Format)
	}
	if opts.OnConflictDoNothing && opts.Format != exportFormatInsertSQL {
		return exportTaskOptions{}, errors.New("on_conflict_do_nothing requires insert_sql export format")
	}
	if len(opts.ValueReplacements) > 0 && opts.Format != exportFormatInsertSQL {
		return exportTaskOptions{}, errors.New("value replacements require insert_sql export format")
	}
	where, err := normalizeExportWhereCondition(opts.Where)
	if err != nil {
		return exportTaskOptions{}, err
	}
	opts.Where = where
	replacements, err := normalizeExportValueReplacements(opts.ValueReplacements)
	if err != nil {
		return exportTaskOptions{}, err
	}
	opts.ValueReplacements = replacements
	return opts, nil
}

func normalizeDataSyncTaskOptions(opts dataSyncTaskOptions) (dataSyncTaskOptions, error) {
	where, err := normalizeExportWhereCondition(opts.Where)
	if err != nil {
		return dataSyncTaskOptions{}, err
	}
	replacements, err := normalizeExportValueReplacements(opts.ValueReplacements)
	if err != nil {
		return dataSyncTaskOptions{}, err
	}
	opts.Where = where
	opts.ValueReplacements = replacements
	return opts, nil
}

func normalizeExportValueReplacements(raw []ExportValueReplacement) ([]ExportValueReplacement, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxExportValueReplacementRuleCount {
		return nil, fmt.Errorf("too many value replacement rules, max %d", maxExportValueReplacementRuleCount)
	}

	seenColumns := make(map[string]struct{}, len(raw))
	totalEntries := 0
	out := make([]ExportValueReplacement, 0, len(raw))
	for i, rule := range raw {
		column := strings.TrimSpace(rule.Column)
		if column == "" {
			return nil, fmt.Errorf("value replacement rule %d requires column", i+1)
		}
		if _, ok := seenColumns[column]; ok {
			return nil, fmt.Errorf("duplicate value replacement column: %s", column)
		}
		seenColumns[column] = struct{}{}

		onMissing := strings.TrimSpace(rule.OnMissing)
		if onMissing == "" {
			onMissing = exportReplacementOnMissingKeep
		}
		switch onMissing {
		case exportReplacementOnMissingKeep, exportReplacementOnMissingEmpty:
		default:
			return nil, fmt.Errorf("unsupported value replacement on_missing: %s", onMissing)
		}

		mapping := make(map[string]string, len(rule.Mapping))
		for k, v := range rule.Mapping {
			mapping[k] = v
		}
		totalEntries += len(mapping)
		if totalEntries > maxExportValueReplacementEntryCount {
			return nil, fmt.Errorf("too many value replacement entries, max %d", maxExportValueReplacementEntryCount)
		}

		out = append(out, ExportValueReplacement{
			Column:    column,
			Mapping:   mapping,
			OnMissing: onMissing,
		})
	}
	return out, nil
}

func normalizeExportWhereCondition(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if len(s) > maxExportWhereLength {
		return "", fmt.Errorf("where condition is too long, max %d characters", maxExportWhereLength)
	}
	if hasWhereKeywordPrefix(s) {
		s = strings.TrimSpace(s[len("where"):])
	}
	if s == "" {
		return "", errors.New("where condition cannot be empty")
	}
	if strings.ContainsRune(s, 0) {
		return "", errors.New("where condition contains invalid character")
	}
	if strings.Contains(s, ";") ||
		strings.Contains(s, "--") ||
		strings.Contains(s, "/*") ||
		strings.Contains(s, "*/") {
		return "", errors.New("where condition cannot contain semicolons or comments")
	}
	if err := validateBalancedSQLQuotes(s); err != nil {
		return "", err
	}
	return s, nil
}

func hasWhereKeywordPrefix(s string) bool {
	if len(s) < len("where") || !strings.EqualFold(s[:len("where")], "where") {
		return false
	}
	if len(s) == len("where") {
		return true
	}
	next := rune(s[len("where")])
	return unicode.IsSpace(next) || next == '('
}

func validateBalancedSQLQuotes(s string) error {
	inSingle := false
	inDouble := false
	inBacktick := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}

		if inSingle {
			if c == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' {
				if next == '"' {
					i++
					continue
				}
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				if next == '`' {
					i++
					continue
				}
				inBacktick = false
			}
			continue
		}

		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		}
	}

	if inSingle {
		return errors.New("where condition has an unterminated string literal")
	}
	if inDouble {
		return errors.New("where condition has an unterminated quoted identifier")
	}
	if inBacktick {
		return errors.New("where condition has an unterminated quoted identifier")
	}
	return nil
}
