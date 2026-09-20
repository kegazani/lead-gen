// Команда importlint проверяет границы импортов между слоями, bounded contexts и
// публичным edge в multi-module workspace lead-gen. В отличие от tools/archlint
// (каталог, JSON Schema, OpenSpec-спеки, диаграммы), этот инструмент читает
// Go-импорты через go/parser.
//
// Грамматика путей (базовый префикс задаётся флагом -module, по умолчанию
// lead-gen — корневого go.mod больше нет, workspace связан go.work):
//
//	lead-gen/backend/contexts/<ctx>/internal/<layer>[/<subpath>] — внутренний слой контекста;
//	lead-gen/backend/contexts/<ctx>[/<subpath>]                  — публичная поверхность (сборка);
//	lead-gen/contracts/<owner>[/<subpath>]                       — опубликованный контракт;
//	lead-gen/backend/platform/...                                — platform;
//	lead-gen/backend/cmd/...                                     — точка входа (composition root);
//	lead-gen/frontend/public/...                                 — публичный edge.
//
// Проверяемые рёбра:
//
//	a) Слои своего контекста: internal/domain не импортирует свой internal/app,
//	   internal/ports, internal/adapters; internal/app не импортирует свой
//	   internal/adapters; internal/ports не импортирует свой internal/adapters.
//	b) Межконтекстно: контекст импортирует ДРУГОЙ контекст только через
//	   lead-gen/contracts/<owner>. Импорт чужого lead-gen/backend/contexts/<owner>/internal/
//	   запрещён; чужое хранилище .../internal/adapters/persistence/** запрещено
//	   отдельной формулировкой; чужой корневой пакет контекста запрещён тоже.
//	c) Публичный edge (lead-gen/frontend/public/...) не импортирует
//	   lead-gen/backend/contexts/... вообще и не импортирует пакеты-адаптеры хранилищ
//	   (любой путь lead-gen/.../adapters/persistence).
//	d) cmd — composition root, не ограничен.
//
// Инструмент использует только стандартную библиотеку и не резолвит импорты:
// достаточно синтаксического разбора (parser.ImportsOnly), поэтому фикстуры в
// testdata не обязаны компилироваться.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// defaultModule — базовый префикс путей пакетов. Корневого go.mod больше нет,
// модули связаны go.work, поэтому значение по умолчанию фиксировано и может
// быть переопределено флагом -module.
const defaultModule = "lead-gen"

// Kind описывает вид пакета в грамматике workspace.
const (
	kindContext  = "context"
	kindContract = "contract"
	kindPlatform = "platform"
	kindCmd      = "cmd"
	kindEdge     = "edge"
)

// Role описывает положение пакета в раскладке workspace.
//
// Kind принимает значения: kindContext для lead-gen/contexts/<ctx>, kindContract
// для lead-gen/contracts/<owner>, kindPlatform, kindCmd, kindEdge; пустое
// значение — пакет вне грамматики (внешний модуль или неизвестный префикс).
//
// Internal == true только для внутреннего слоя контекста
// (lead-gen/contexts/<ctx>/internal/<layer>/...); для публичной поверхности
// контекста и остальных Kind false.
//
// Subpath хранит оставшийся путь после Layer (например, "persistence/postgres"
// для .../adapters/persistence/postgres) и нужен, чтобы отличать хранилище от
// прочих адаптеров и опубликованный контракт от прочих подпакетов владельца.
type Role struct {
	Kind     string
	Internal bool
	Context  string
	Owner    string
	Layer    string
	Subpath  string
}

// Violation — одно запрещённое ребро импорта.
type Violation struct {
	File     string
	Importer string
	Imported string
	Reason   string
}

// Analysis — результат обхода дерева.
type Analysis struct {
	Files      int
	Packages   int
	Violations []Violation
}

// isPersistence сообщает, что подпуть слоя — хранилище (persistence/**).
func isPersistence(subpath string) bool {
	return subpath == "persistence" || strings.HasPrefix(subpath, "persistence/")
}

// isStorageAdapter сообщает, что пакет — адаптер хранилища
// (любой путь lead-gen/.../adapters/persistence...).
func isStorageAdapter(role Role) bool {
	return role.Kind == kindContext && role.Layer == "adapters" && isPersistence(role.Subpath)
}

// crossContextReason объясняет запрет импорта чужого контекста. Контекст вправе
// импортировать другой контекст только через опубликованный контракт владельца
// lead-gen/contracts/<owner>, поэтому все ветви здесь — нарушения.
func crossContextReason(importerRole, importedRole Role) string {
	if isStorageAdapter(importedRole) {
		return fmt.Sprintf("чужое хранилище: контекст %q импортирует adapters/persistence контекста %q",
			importerRole.Context, importedRole.Context)
	}
	if importedRole.Internal {
		return fmt.Sprintf("чужой internal контекста: контекст %q импортирует internal контекста %q; межконтекстный доступ только через lead-gen/contracts/<owner>",
			importerRole.Context, importedRole.Context)
	}
	return fmt.Sprintf("чужой корневой пакет контекста: контекст %q импортирует контекст %q; межконтекстный доступ только через lead-gen/contracts/<owner>",
		importerRole.Context, importedRole.Context)
}

// edgeReason объясняет запреты публичного edge: он не импортирует контексты
// вообще и адаптеры хранилищ в частности.
func edgeReason(importedRole Role) string {
	if importedRole.Kind != kindContext {
		return ""
	}
	if isStorageAdapter(importedRole) {
		return fmt.Sprintf("публичный edge не импортирует адаптеры хранилища контекста %q", importedRole.Context)
	}
	return fmt.Sprintf("публичный edge не импортирует контексты (нарушен контекст %q)", importedRole.Context)
}

// forbiddenLayer возвращает причину запрета, если пакет importer с ролью
// importerRole импортирует пакет с ролью importedRole. Пустая строка — ребро
// допустимо.
func forbiddenLayer(importerRole, importedRole Role) string {
	// cmd — composition root, границами не ограничен.
	if importerRole.Kind == kindCmd {
		return ""
	}

	// Публичный edge ограничен собственными правилами.
	if importerRole.Kind == kindEdge {
		return edgeReason(importedRole)
	}

	// Остальные правила действуют только для контекстов; platform и пакеты вне
	// грамматики не ограничены.
	if importerRole.Kind != kindContext {
		return ""
	}

	// Межконтекстные импорты разрешены только через lead-gen/contracts/<owner>.
	if importedRole.Kind == kindContext && importedRole.Context != importerRole.Context {
		return crossContextReason(importerRole, importedRole)
	}

	// Слои своего контекста — только для внутренних слоёв.
	if !importerRole.Internal {
		return ""
	}
	if importedRole.Kind != kindContext || importedRole.Context != importerRole.Context || !importedRole.Internal {
		return ""
	}

	switch importerRole.Layer {
	case "domain":
		switch importedRole.Layer {
		case "app", "ports", "adapters":
			return fmt.Sprintf("domain не импортирует слой %q своего контекста", importedRole.Layer)
		}
	case "app":
		if importedRole.Layer == "adapters" {
			return "app не импортирует adapters своего контекста"
		}
	case "ports":
		if importedRole.Layer == "adapters" {
			return "ports не импортирует adapters своего контекста"
		}
	}
	return ""
}

// parseRole разбирает путь пакета относительно базового префикса модуля.
func parseRole(module, importPath string) Role {
	prefix := module + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return Role{}
	}
	segments := strings.Split(strings.TrimPrefix(importPath, prefix), "/")
	if len(segments) == 0 || segments[0] == "" {
		return Role{}
	}
	switch segments[0] {
	case "backend":
		if len(segments) < 2 {
			return Role{}
		}
		return parseBackendRole(segments[1:])
	case "contracts":
		role := Role{Kind: kindContract}
		if len(segments) >= 2 {
			role.Owner = segments[1]
		}
		if len(segments) >= 3 {
			role.Subpath = strings.Join(segments[2:], "/")
		}
		return role
	case "frontend":
		role := Role{Kind: kindEdge}
		if len(segments) >= 2 {
			role.Subpath = strings.Join(segments[1:], "/")
		}
		return role
	default:
		return Role{}
	}
}

// parseBackendRole разбирает путь служебного кода после сегмента backend:
// contexts/<ctx>[/internal/<layer>[/<subpath>]], platform, cmd.
func parseBackendRole(segments []string) Role {
	switch segments[0] {
	case "contexts":
		role := Role{Kind: kindContext}
		if len(segments) >= 2 {
			role.Context = segments[1]
		}
		if len(segments) >= 3 && segments[2] == "internal" {
			role.Internal = true
			if len(segments) >= 4 {
				role.Layer = segments[3]
			}
			if len(segments) >= 5 {
				role.Subpath = strings.Join(segments[4:], "/")
			}
			return role
		}
		if len(segments) >= 3 {
			role.Subpath = strings.Join(segments[2:], "/")
		}
		return role
	case "platform":
		return Role{Kind: kindPlatform}
	case "cmd":
		return Role{Kind: kindCmd}
	default:
		return Role{}
	}
}

// importPathOf строит путь пакета по каталогу файла относительно базового
// префикса и корня workspace.
func importPathOf(module, root, filePath string) string {
	relative, err := filepath.Rel(root, filepath.Dir(filePath))
	if err != nil {
		return ""
	}
	relative = filepath.ToSlash(relative)
	if relative == "." {
		return module
	}
	return module + "/" + relative
}

// skipDir сообщает, надо ли пропускать каталог при обходе. testdata исключается,
// чтобы намеренно некорректные фикстуры не давали ложных срабатываний на
// реальном дереве.
func skipDir(name string) bool {
	if name == "testdata" || name == "vendor" {
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// analyze обходит Go-файлы под root и возвращает найденные нарушения.
func analyze(root, module string) (Analysis, error) {
	analysis := Analysis{}
	packages := make(map[string]struct{})
	fileSet := token.NewFileSet()

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		importerPath := importPathOf(module, root, path)
		importerRole := parseRole(module, importerPath)
		analysis.Files++
		packages[importerPath] = struct{}{}

		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			analysis.Violations = append(analysis.Violations, Violation{
				File: path, Importer: importerPath, Imported: "", Reason: "ошибка разбора: " + err.Error(),
			})
			return nil
		}
		for _, spec := range parsed.Imports {
			importedPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			importedRole := parseRole(module, importedPath)
			if reason := forbiddenLayer(importerRole, importedRole); reason != "" {
				analysis.Violations = append(analysis.Violations, Violation{
					File: path, Importer: importerPath, Imported: importedPath, Reason: reason,
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		return analysis, walkErr
	}
	analysis.Packages = len(packages)
	sort.SliceStable(analysis.Violations, func(i, j int) bool {
		if analysis.Violations[i].File != analysis.Violations[j].File {
			return analysis.Violations[i].File < analysis.Violations[j].File
		}
		return analysis.Violations[i].Imported < analysis.Violations[j].Imported
	})
	return analysis, nil
}

// readModulePath читает базовый префикс из go.mod, откатываясь к defaultModule.
// Корневого go.mod в workspace нет, поэтому на реальном дереве возвращается
// defaultModule.
func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return defaultModule
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			if value := strings.TrimSpace(strings.TrimPrefix(line, "module ")); value != "" {
				return value
			}
		}
	}
	return defaultModule
}

// defaultRoot поднимается вверх от рабочего каталога до корня workspace
// (каталог с go.work). Корневого go.mod больше нет, поэтому маркер — go.work.
func defaultRoot() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "."
	}
	directory := workingDirectory
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.work")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return workingDirectory
		}
		directory = parent
	}
}

func main() {
	rootFlag := flag.String("root", defaultRoot(), "корень workspace (вверх до каталога с go.work)")
	moduleFlag := flag.String("module", defaultModule, "базовый префикс путей пакетов")
	flag.Parse()

	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "importlint:", err)
		os.Exit(1)
	}
	module := strings.TrimSuffix(*moduleFlag, "/")
	if module == "" {
		module = defaultModule
	}
	analysis, err := analyze(root, module)
	if err != nil {
		fmt.Fprintln(os.Stderr, "importlint:", err)
		os.Exit(1)
	}

	if len(analysis.Violations) > 0 {
		fmt.Fprintf(os.Stderr, "importlint: нарушений границ импортов: %d\n", len(analysis.Violations))
		for _, violation := range analysis.Violations {
			if violation.Imported == "" {
				fmt.Fprintf(os.Stderr, "- %s: %s\n", violation.File, violation.Reason)
				continue
			}
			fmt.Fprintf(os.Stderr, "- %s: %s -> %s (%s)\n",
				violation.File, violation.Importer, violation.Imported, violation.Reason)
		}
		os.Exit(1)
	}

	fmt.Printf("importlint: OK — пакетов: %d, файлов: %d, запрещённых рёбер: 0\n",
		analysis.Packages, analysis.Files)
}
