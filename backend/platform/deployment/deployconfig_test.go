package deployment

import (
	"os"
	"testing"

	composition "lead-gen/backend/platform/composition"
)

// TestParseDeployConfigRealFile проверяет минимальный парсер на настоящем
// deploy/services.yaml. Тест держит инварианты модели, а не точный перечень
// групп и сервисов, чтобы следующая правка конфигурации не ломала его:
//   - механизм каждой группы — in-process;
//   - роли сервиса разобраны и известны composition.ParseRole;
//   - роль сервиса встречается ровно в одном процессе (роль — единица
//     размещения), хотя сам сервис MAY входить в несколько процессов;
//   - ни один процесс не смешивает роли sender и api: контур отправки отделён
//     от точки входа api.
func TestParseDeployConfigRealFile(t *testing.T) {
	data, err := os.ReadFile("../../../deploy/services.yaml")
	if err != nil {
		t.Fatalf("чтение deploy/services.yaml: %v", err)
	}
	cfg, err := ParseDeployConfig(data)
	if err != nil {
		t.Fatalf("разбор deploy/services.yaml: %v", err)
	}
	if len(cfg.Processes) == 0 {
		t.Fatal("не объявлено ни одной группы процессов")
	}

	seenProcesses := map[string]bool{}
	// Сервис → роль → группа, где роль объявлена впервые.
	roleHome := map[string]map[composition.Role]string{}
	distinctServices := map[string]bool{}
	for _, process := range cfg.Processes {
		if seenProcesses[process.Name] {
			t.Errorf("группа %q объявлена дважды", process.Name)
		}
		seenProcesses[process.Name] = true

		if process.Mechanism != "in-process" {
			t.Errorf("группа %q: mechanism %q, ожидался in-process", process.Name, process.Mechanism)
		}
		if len(process.Services) == 0 {
			t.Errorf("группа %q без сервисов", process.Name)
		}

		rolesInProcess := map[composition.Role]bool{}
		for _, service := range process.Services {
			distinctServices[service.ID] = true
			if len(service.Roles) == 0 {
				t.Errorf("сервис %q без ролей", service.ID)
			}
			if roleHome[service.ID] == nil {
				roleHome[service.ID] = map[composition.Role]string{}
			}
			for _, raw := range service.Roles {
				role, err := composition.ParseRole(raw)
				if err != nil {
					t.Errorf("сервис %q: %v", service.ID, err)
					continue
				}
				if home, ok := roleHome[service.ID][role]; ok {
					t.Errorf("сервис %q: роль %q объявлена и в группе %q, и в %q — роль принадлежит ровно одному процессу",
						service.ID, role, home, process.Name)
				}
				roleHome[service.ID][role] = process.Name
				rolesInProcess[role] = true
			}
		}
		if rolesInProcess[composition.RoleSender] && rolesInProcess[composition.RoleAPI] {
			t.Errorf("группа %q смешивает роли sender и api: контур отправки должен быть отделён от точки входа api",
				process.Name)
		}
	}
	if len(distinctServices) == 0 {
		t.Fatal("не объявлено ни одного сервиса")
	}
}

// TestParseDeployConfigRejectsUnknownLine фиксирует строгость парсера: лишняя
// строка — ошибка с номером, а не молчаливый пропуск.
func TestParseDeployConfigRejectsUnknownLine(t *testing.T) {
	_, err := ParseDeployConfig([]byte("processes:\n  - name: core\n    unknown: 1\n"))
	if err == nil {
		t.Fatal("нераспознанная строка не отвергнута")
	}
}
