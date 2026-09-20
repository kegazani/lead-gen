// Package composition — composition root: общий контракт сервисной сборки и
// запуска её ролей.
//
// Каждый сервис каталога собирается в собственном пакете
// contexts/<context-id> (design.md, Решение 6): он соединяет
// адаптеры, порты и use cases только своего контекста и публикует свои роли.
// Межконтекстные импорты contexts/<ctx>/internal допустимы только внутри сборки
// того сервиса, которому принадлежит контекст; сервисы соединяются транспортом.
//
// Этот пакет ничего не знает о контекстах: он задаёт роль запуска, раннер и
// интерфейс сервиса, а точку входа cmd/lead-gen связывает их с deploy/services.yaml.
// Роль владеет только запуском и адаптерами, не доменом.
package composition

import (
	"context"
	"fmt"
	"strings"
)

// Role — роль запуска сервиса (design.md, Решение 6). Роль — режим запуска
// внутри сервиса, а не единица размещения.
type Role string

const (
	// RoleAPI — внутренний HTTP-сервер контекста.
	RoleAPI Role = "api"
	// RoleIngest — публичный HTTP-сервер приёма на отдельном домене.
	RoleIngest Role = "ingest"
	// RoleSender — потребитель утверждённых действий и исходящий адаптер.
	RoleSender Role = "sender"
	// RoleWorker — потребители очередей движков контекста.
	RoleWorker Role = "worker"
	// RoleChains — durable-планировщик долгих состояний.
	RoleChains Role = "chains"
	// RoleSchedule — тикер, порождающий команды идемпотентными окнами.
	RoleSchedule Role = "schedule"
)

// knownRoles — известные роли в порядке таблицы design.md, Решение 6.
var knownRoles = []Role{RoleAPI, RoleIngest, RoleSender, RoleWorker, RoleChains, RoleSchedule}

// ParseRole разбирает текстовую роль из конфигурации развёртывания. Неизвестная
// роль — ошибка, а не молчаливый пропуск: расхождение конфигурации и модели
// должно быть видно на старте.
func ParseRole(s string) (Role, error) {
	trimmed := strings.TrimSpace(s)
	for _, role := range knownRoles {
		if Role(trimmed) == role {
			return role, nil
		}
	}
	return "", fmt.Errorf("composition: неизвестная роль запуска %q", s)
}

// RoleRunner запускает одну роль сервиса. Run блокируется до отмены контекста;
// nil означает штатное завершение (контекст отменён). Роль владеет только
// запуском и адаптерами, домен остаётся за контекстом.
type RoleRunner interface {
	Run(ctx context.Context) error
}

// RoleFunc — адаптер функции к RoleRunner.
type RoleFunc func(ctx context.Context) error

// Run реализует RoleRunner. Нулевая функция завершается сразу.
func (f RoleFunc) Run(ctx context.Context) error {
	if f == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return f(ctx)
}

// Service — сервисная сборка: идентификатор сервиса и раннеры его ролей.
// Реализуется пакетом contexts/<context-id>.
type Service interface {
	// ID возвращает идентификатор сервиса, равный идентификатору контекста.
	ID() string
	// RoleRunner возвращает раннер роли. Второе значение — false, если роль
	// сервисом не реализована: вызывающий обязан честно залогировать пропуск, а
	// не делать вид, что роль запущена.
	RoleRunner(role Role) (RoleRunner, bool)
}
