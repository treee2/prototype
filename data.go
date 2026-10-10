package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxFileSize = 10 << 20 // 10 МиБ
	maxRows     = 100_000
)

// Схема предметной области: признаки и допустимые значения.
var schema = map[string][]string{
	"temperature": {"low", "normal", "high"},
	"noise":       {"quiet", "normal", "loud"},
	"smoke":       {"no", "yes"},
	"vibration":   {"low", "normal", "high"},
}

// Порядок признаков фиксирован, чтобы ключи и правила были детерминированы.
var featureOrder = []string{"temperature", "noise", "smoke", "vibration"}

// Допустимые диагнозы (метки для экспертной системы).
var faultLabels = []string{"ok", "overheating", "bearing_wear", "short_circuit"}

// Record — одно наблюдение с известным диагнозом.
type Record struct {
	ID       int               `json:"id"`
	Features map[string]string `json:"features"`
	Fault    string            `json:"fault"`
}

// key — каноническое представление набора признаков.
func (r Record) key() string {
	var b strings.Builder
	for _, f := range featureOrder {
		b.WriteString(f)
		b.WriteByte('=')
		b.WriteString(r.Features[f])
		b.WriteByte(';')
	}
	return b.String()
}

// loadCSV читает и проверяет CSV. Возвращает корректные записи и список ошибок значений.
func loadCSV(path string) ([]Record, []string, error) {
	clean := filepath.Clean(path)
	if strings.ToLower(filepath.Ext(clean)) != ".csv" {
		return nil, nil, errors.New("входной файл должен иметь расширение .csv")
	}

	info, err := os.Stat(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("доступ к файлу: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("ожидается обычный файл")
	}
	if info.Size() > maxFileSize {
		return nil, nil, fmt.Errorf("файл больше %d МиБ", maxFileSize>>20)
	}

	f, err := os.Open(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("открытие файла: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("чтение заголовка: %w", err)
	}
	idx, err := columnIndex(header)
	if err != nil {
		return nil, nil, err
	}
	r.FieldsPerRecord = len(header) // строки с лишними/недостающими полями — ошибка парсера

	var recs []Record
	var issues []string
	for n := 1; ; n++ {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("запись %d: %w", n, err)
		}
		if n > maxRows {
			return nil, nil, fmt.Errorf("слишком много записей (максимум %d)", maxRows)
		}

		rec := Record{ID: n, Features: make(map[string]string, len(featureOrder))}
		valid := true
		for _, f := range featureOrder {
			v := normalize(row[idx[f]])
			if !slices.Contains(schema[f], v) {
				issues = append(issues, fmt.Sprintf("запись %d: %s=%q недопустимо", n, f, v))
				valid = false
			}
			rec.Features[f] = v
		}
		rec.Fault = normalize(row[idx["fault"]])
		if !slices.Contains(faultLabels, rec.Fault) {
			issues = append(issues, fmt.Sprintf("запись %d: диагноз %q недопустим", n, rec.Fault))
			valid = false
		}
		if valid {
			recs = append(recs, rec)
		}
	}
	return recs, issues, nil
}

// loadClean загружает данные и требует отсутствия ошибок значений и противоречий.
func loadClean(path string) ([]Record, error) {
	recs, issues, err := loadCSV(path)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("найдено ошибок значений: %d; выполните validate", len(issues))
	}
	if c := findConflicts(recs); len(c) > 0 {
		return nil, fmt.Errorf("найдены противоречия (%d); выполните validate", len(c))
	}
	return recs, nil
}

func columnIndex(header []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		name := strings.TrimPrefix(normalize(h), "\ufeff") // BOM в начале файла
		if _, dup := idx[name]; dup {
			return nil, fmt.Errorf("дублирующийся столбец %q", name)
		}
		idx[name] = i
	}
	required := append(slices.Clone(featureOrder), "fault")
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("отсутствует обязательный столбец %q", c)
		}
	}
	return idx, nil
}

// dedupe удаляет полностью совпадающие записи и возвращает число удалённых.
func dedupe(recs []Record) ([]Record, int) {
	seen := make(map[string]bool, len(recs))
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		k := r.key() + "->" + r.Fault
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out, len(recs) - len(out)
}

// findConflicts ищет одинаковые наборы признаков с разными диагнозами.
func findConflicts(recs []Record) []string {
	labels := make(map[string]map[string]bool)
	for _, r := range recs {
		k := r.key()
		if labels[k] == nil {
			labels[k] = make(map[string]bool)
		}
		labels[k][r.Fault] = true
	}
	var msgs []string
	for _, k := range sortedKeys(labels) {
		if len(labels[k]) > 1 {
			msgs = append(msgs, fmt.Sprintf("одинаковые признаки (%s) с разными диагнозами: %s",
				k, strings.Join(sortedKeys(labels[k]), ", ")))
		}
	}
	return msgs
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
