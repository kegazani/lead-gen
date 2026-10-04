package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// ContentPackageRepositoryFactory создаёт репозиторий для одного прогона suite.
//
// Фабрика обязана вернуть репозиторий с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов (очистку таблиц): suite
// вызывает фабрику перед каждым подтестом и ничего не знает о хранилище.
type ContentPackageRepositoryFactory func(t *testing.T) ports.ContentPackageRepository

// RunContentPackageRepositoryContract проверяет, что реализация
// ContentPackageRepository соблюдает контракт порта: сохраняет и загружает
// агрегат целиком, различает отсутствие пакета, отвергает устаревшее сохранение
// конфликтом версий и не создаёт второй пакет по занятому идентификатору.
func RunContentPackageRepositoryContract(t *testing.T, newRepository ContentPackageRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		pkg := mustPackage(t, "asset-roundtrip")
		if err := repository.Save(ctx, pkg, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, pkg.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertPackage(t, loaded, pkg)
	})

	t.Run("отсутствие пакета", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "asset-missing")
		if err == nil {
			t.Fatal("FindByID отсутствующего пакета вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrContentPackageNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrContentPackageNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего пакета ожидался nil, получено %v", loaded)
		}
	})

	t.Run("оптимистичная блокировка", func(t *testing.T) {
		repository := newRepository(t)
		pkg := mustPackage(t, "asset-lock")
		if err := repository.Save(ctx, pkg, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}

		stale, err := repository.FindByID(ctx, pkg.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		fresh, err := repository.FindByID(ctx, pkg.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}

		expected := fresh.Version()
		if _, err := fresh.PublishVersion(domain.PublishVersionInput{
			VersionID: "ver-lock-2", Body: "Новый текст версии.", Actor: "actor-1", At: at(1), EventID: "evt-pub-lock",
		}); err != nil {
			t.Fatalf("PublishVersion: %v", err)
		}
		if err := repository.Save(ctx, fresh, expected); err != nil {
			t.Fatalf("Save свежей версии: %v", err)
		}

		if err := repository.Save(ctx, stale, expected); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}

		reloaded, err := repository.FindByID(ctx, pkg.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if reloaded.Version() != expected+1 {
			t.Fatalf("версия после изменения = %d, ожидалась %d", reloaded.Version(), expected+1)
		}
		if len(reloaded.Versions()) != 2 {
			t.Fatalf("число версий после публикации = %d, ожидалось 2", len(reloaded.Versions()))
		}
	})

	t.Run("создание с ненулевой ожидаемой версией", func(t *testing.T) {
		repository := newRepository(t)
		pkg := mustPackage(t, "asset-new-version")
		if err := repository.Save(ctx, pkg, 5); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("ошибка = %v, ожидалась ErrVersionConflict", err)
		}
		if _, err := repository.FindByID(ctx, pkg.ID()); !errors.Is(err, ports.ErrContentPackageNotFound) {
			t.Fatalf("отвергнутое создание оставило пакет: ошибка = %v", err)
		}
	})

	t.Run("повтор создания по занятому идентификатору", func(t *testing.T) {
		repository := newRepository(t)
		pkg := mustPackage(t, "asset-dup")
		if err := repository.Save(ctx, pkg, 0); err != nil {
			t.Fatalf("первое Save: %v", err)
		}
		if err := repository.Save(ctx, mustPackage(t, "asset-dup"), 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("повторное создание: ошибка = %v, ожидалась ErrVersionConflict", err)
		}
	})
}
