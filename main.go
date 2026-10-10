package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usage = `expertprep — подготовка данных для экспертной системы
диагностики неисправностей электродвигателей.

Команды:
  validate  -in data.csv                      проверить данные и противоречия
  prepare   -in data.csv -out dataset.json    очистить, удалить дубликаты, сохранить JSON
  rules     -in data.csv -out rules.json      построить правила ЕСЛИ-ТО для базы знаний

Флаги rules:
  -min-support     минимум подтверждающих наблюдений (по умолчанию 2)
  -min-confidence  минимальная доля диагноза в группе, (0, 1] (по умолчанию 0.8)

Общие флаги:
  -force  перезаписать существующий выходной файл
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "validate":
		return cmdValidate(args[1:], out)
	case "prepare":
		return cmdPrepare(args[1:], out)
	case "rules":
		return cmdRules(args[1:], out)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	default:
		return fmt.Errorf("неизвестная команда %q; см. help", args[0])
	}
}

func cmdValidate(args []string, out io.Writer) error {
	fset := flag.NewFlagSet("validate", flag.ContinueOnError)
	in := fset.String("in", "", "входной CSV-файл")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("укажите -in")
	}

	recs, issues, err := loadCSV(*in)
	if err != nil {
		return err
	}
	conflicts := findConflicts(recs)

	fmt.Fprintf(out, "Корректных записей: %d\n", len(recs))
	fmt.Fprintf(out, "Ошибок значений:    %d\n", len(issues))
	for _, s := range issues {
		fmt.Fprintln(out, "  -", s)
	}
	fmt.Fprintf(out, "Противоречий:       %d\n", len(conflicts))
	for _, s := range conflicts {
		fmt.Fprintln(out, "  -", s)
	}
	if len(issues) > 0 || len(conflicts) > 0 {
		return errors.New("данные требуют исправления")
	}
	fmt.Fprintln(out, "Данные корректны.")
	return nil
}

func cmdPrepare(args []string, out io.Writer) error {
	fset := flag.NewFlagSet("prepare", flag.ContinueOnError)
	in := fset.String("in", "", "входной CSV-файл")
	dst := fset.String("out", "", "выходной JSON-файл")
	force := fset.Bool("force", false, "перезаписать выходной файл")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if *in == "" || *dst == "" {
		return errors.New("укажите -in и -out")
	}
	if filepath.Clean(*in) == filepath.Clean(*dst) {
		return errors.New("выходной файл не должен совпадать с входным")
	}

	recs, err := loadClean(*in)
	if err != nil {
		return err
	}
	clean, removed := dedupe(recs)

	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация: %w", err)
	}
	if err := writeOutput(*dst, data, *force); err != nil {
		return err
	}
	fmt.Fprintf(out, "Сохранено записей: %d, удалено дубликатов: %d → %s\n",
		len(clean), removed, filepath.Clean(*dst))
	return nil
}

func cmdRules(args []string, out io.Writer) error {
	fset := flag.NewFlagSet("rules", flag.ContinueOnError)
	in := fset.String("in", "", "входной CSV-файл")
	dst := fset.String("out", "", "выходной JSON-файл с правилами")
	minSup := fset.Int("min-support", 2, "минимум подтверждающих наблюдений")
	minConf := fset.Float64("min-confidence", 0.8, "минимальная доля диагноза в группе (0, 1]")
	force := fset.Bool("force", false, "перезаписать выходной файл")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if *in == "" || *dst == "" {
		return errors.New("укажите -in и -out")
	}
	if *minSup < 1 {
		return errors.New("-min-support должен быть >= 1")
	}
	if *minConf <= 0 || *minConf > 1 {
		return errors.New("-min-confidence должен быть в диапазоне (0, 1]")
	}

	// Дубликаты здесь не удаляются: повторные наблюдения подтверждают правило.
	recs, err := loadClean(*in)
	if err != nil {
		return err
	}
	rules := buildRules(recs, *minSup, *minConf)
	if len(rules) == 0 {
		fmt.Fprintln(out, "Правила не найдены: попробуйте уменьшить -min-support или -min-confidence.")
	}

	set := RuleSet{Source: filepath.Base(*in), Count: len(rules), Rules: rules}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация: %w", err)
	}
	if err := writeOutput(*dst, data, *force); err != nil {
		return err
	}
	fmt.Fprintf(out, "Построено правил: %d → %s\n", len(rules), filepath.Clean(*dst))
	return nil
}

// writeOutput пишет JSON с правами 0600. Без force не перезаписывает существующий файл
// и не следует по символическим ссылкам (O_EXCL).
func writeOutput(path string, data []byte, force bool) error {
	clean := filepath.Clean(path)
	if strings.ToLower(filepath.Ext(clean)) != ".json" {
		return errors.New("выходной файл должен иметь расширение .json")
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(clean, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("файл %s уже существует (используйте -force)", clean)
		}
		return fmt.Errorf("создание файла: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("запись файла: %w", err)
	}
	return f.Close()
}
