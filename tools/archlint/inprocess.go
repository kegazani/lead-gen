package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// InProcessAssembly описывает отдельный opt-in deployment и реальные публичные
// порты, собираемые composition root. Наличие файла не доказывает live readiness.
type InProcessAssembly struct {
	Version         int                `json:"version"`
	ID              string             `json:"id"`
	Process         string             `json:"process"`
	ActivationFlag  string             `json:"activation_flag"`
	Deployment      string             `json:"deployment"`
	CompositionRoot string             `json:"composition_root"`
	RequiredRoles   []DeployService    `json:"required_roles"`
	Bindings        []InProcessBinding `json:"bindings"`
}
type InProcessBinding struct {
	Owner           string `json:"owner"`
	Contract        string `json:"contract"`
	InterfaceFile   string `json:"interface_file"`
	InterfaceName   string `json:"interface_name"`
	ConstructorFile string `json:"constructor_file"`
	Constructor     string `json:"constructor"`
}

var assemblyID = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func validateInProcessBindings(root string, catalog Catalog) []string {
	var problems []string
	for _, relation := range catalog.Relationships {
		if relation.Mechanism != mechanismInProcess {
			continue
		}
		fail := func(err error) {
			problems = append(problems, fmt.Sprintf("relationships.%s: in-process: %v", relation.ID, err))
		}
		if relation.Transport != transportSync || (relation.Integration != "command" && relation.Integration != "query") {
			fail(fmt.Errorf("разрешён только sync command/query"))
			continue
		}
		if !assemblyID.MatchString(relation.CoLocation) {
			fail(fmt.Errorf("не объявлен допустимый co_location"))
			continue
		}
		var assembly InProcessAssembly
		raw, err := os.ReadFile(filepath.Join(root, "deploy", relation.CoLocation+".json"))
		if err != nil {
			fail(fmt.Errorf("deployment-описание отсутствует: %w", err))
			continue
		}
		if err = json.Unmarshal(raw, &assembly); err != nil {
			fail(err)
			continue
		}
		if err = validateInProcessAssembly(root, catalog, relation, assembly); err != nil {
			fail(err)
		}
	}
	return problems
}

func validateInProcessAssembly(root string, catalog Catalog, relation Relationship, assembly InProcessAssembly) error {
	if assembly.Version != 1 || assembly.ID != relation.CoLocation || assembly.Process == "" || assembly.ActivationFlag == "" || len(assembly.RequiredRoles) == 0 {
		return fmt.Errorf("неполное deployment-описание")
	}
	deployment, err := repositoryPath(root, assembly.Deployment, "deploy")
	if err != nil {
		return err
	}
	config, err := loadDeploy(deployment)
	if err != nil {
		return err
	}
	var process *DeployProcess
	for i := range config.Processes {
		if config.Processes[i].Name == assembly.Process {
			process = &config.Processes[i]
			break
		}
	}
	if process == nil || process.Mechanism != mechanismInProcess {
		return fmt.Errorf("процесс %q не объявлен как in-process", assembly.Process)
	}
	contexts := map[string]Context{}
	for _, item := range catalog.Contexts {
		contexts[item.ID] = item
	}
	deployed := map[string]map[string]bool{}
	processRoles := map[string]bool{}
	for _, service := range process.Services {
		owner, ok := contexts[service.ID]
		if !ok {
			return fmt.Errorf("неизвестный сервис %q", service.ID)
		}
		deployed[service.ID] = stringSet(service.Roles)
		if len(service.Roles) == 0 {
			return fmt.Errorf("у сервиса %q нет ролей", service.ID)
		}
		for _, role := range service.Roles {
			if !stringSet(owner.Roles)[role] {
				return fmt.Errorf("сервис %q не объявляет роль %q", service.ID, role)
			}
			processRoles[role] = true
		}
	}
	if processRoles[roleSender] && processRoles[roleAPI] {
		return fmt.Errorf("in-process процесс не совмещает sender с API")
	}
	for _, required := range assembly.RequiredRoles {
		if len(required.Roles) == 0 {
			return fmt.Errorf("обязательные роли %q не заданы", required.ID)
		}
		for _, role := range required.Roles {
			if !deployed[required.ID][role] {
				return fmt.Errorf("обязательная роль %s/%s отсутствует в deployment", required.ID, role)
			}
		}
	}
	owner, ok := contexts[relation.Upstream]
	if !ok || !stringSet(owner.Contracts)[relation.Contract] {
		return fmt.Errorf("контракт не опубликован владельцем")
	}
	compositionPath, err := repositoryPath(root, assembly.CompositionRoot, "backend/cmd")
	if err != nil {
		return err
	}
	composition, err := parser.ParseFile(token.NewFileSet(), compositionPath, nil, 0)
	if err != nil {
		return err
	}
	if _, ok := deployed[relation.Downstream]; !ok {
		// Публичный конструктор библиотеки контекста также подтверждает
		// размещение потребителя, не добавляя ему фиктивную runtime-роль.
		consumer, declared := contexts[relation.Downstream]
		composed := false
		for _, binding := range assembly.Bindings {
			if declared && binding.Owner == relation.Downstream && stringSet(consumer.Contracts)[binding.Contract] && validateOwnerBinding(root, composition, binding) == nil {
				composed = true
				break
			}
		}
		if !composed {
			return fmt.Errorf("потребитель %q не размещён в процессе", relation.Downstream)
		}
	}
	for _, binding := range assembly.Bindings {
		if binding.Owner != relation.Upstream || binding.Contract != relation.Contract {
			continue
		}
		if err := validateOwnerBinding(root, composition, binding); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("нет подключения опубликованного интерфейса владельца %s/%s", relation.Upstream, relation.Contract)
}

func repositoryPath(root, relative, prefix string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(relative))
	if filepath.IsAbs(relative) || !strings.HasPrefix(clean, prefix+"/") {
		return "", fmt.Errorf("путь %q вне разрешённого раздела %s", relative, prefix)
	}
	candidate := filepath.Join(root, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("путь покидает repository")
	}
	return candidate, nil
}

func validateOwnerBinding(root string, composition *ast.File, binding InProcessBinding) error {
	contractPath, err := repositoryPath(root, binding.InterfaceFile, "contracts/"+binding.Owner)
	if err != nil {
		return err
	}
	contract, err := parser.ParseFile(token.NewFileSet(), contractPath, nil, 0)
	if err != nil {
		return err
	}
	found := false
	ast.Inspect(contract, func(node ast.Node) bool {
		if spec, ok := node.(*ast.TypeSpec); ok && spec.Name.Name == binding.InterfaceName {
			_, found = spec.Type.(*ast.InterfaceType)
		}
		return true
	})
	if !found {
		return fmt.Errorf("опубликованный интерфейс %q отсутствует", binding.InterfaceName)
	}
	constructorPath, err := repositoryPath(root, binding.ConstructorFile, "backend/contexts/"+binding.Owner)
	if err != nil {
		return err
	}
	if filepath.Dir(filepath.ToSlash(binding.ConstructorFile)) != "backend/contexts/"+binding.Owner || !ast.IsExported(binding.Constructor) {
		return fmt.Errorf("конструктор должен принадлежать публичному пакету владельца")
	}
	owner, err := parser.ParseFile(token.NewFileSet(), constructorPath, nil, 0)
	if err != nil {
		return err
	}
	found = false
	for _, decl := range owner.Decls {
		if function, ok := decl.(*ast.FuncDecl); ok && function.Recv == nil && function.Name.Name == binding.Constructor {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("публичный конструктор %q отсутствует", binding.Constructor)
	}
	alias := ""
	for _, imp := range composition.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "lead-gen/backend/contexts/"+binding.Owner {
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				alias = owner.Name.Name
			}
		}
	}
	if alias == "" || alias == "." || alias == "_" {
		return fmt.Errorf("composition root не импортирует публичный пакет владельца")
	}
	found = false
	ast.Inspect(composition, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := selector.X.(*ast.Ident)
		if ok && id.Name == alias && selector.Sel.Name == binding.Constructor {
			found = true
		}
		return true
	})
	if !found {
		return fmt.Errorf("composition root не вызывает %s.%s", binding.Owner, binding.Constructor)
	}
	return nil
}
