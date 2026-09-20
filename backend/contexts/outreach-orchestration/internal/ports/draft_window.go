package ports

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Загрузка окна черновиков.
//
// Граница агрегата campaign не меняется (design.md, Решение 6): кампания
// остаётся владельцем черновиков и решений, но при 50 000 черновиков агрегат не
// загружается целиком. Репозиторий отдаёт окно черновиков — детерминированный
// срез в порядке (материал, сегмент, идентификатор черновика) с ограничением по
// лимиту, — а пакетную обработку ведёт агрегат по чанкам.
//
// Загрузка окна — аддитивное расширение порта репозитория кампаний: интерфейс
// CampaignRepository не меняется, поэтому существующие реализации Save/FindByID
// продолжают компилироваться. Реализация, поддерживающая пакетный путь,
// дополняется методом LoadDraftWindow и удовлетворяет DraftWindowRepository.
var (
	// ErrInvalidDraftWindowLimit — лимит окна не положителен. Лимит задаёт
	// конфигурация (design.md, Решение 7), порт его только принимает.
	ErrInvalidDraftWindowLimit = errors.New("порты: лимит окна черновиков должен быть положительным")

	// ErrInvalidDraftWindowQuery — запрос окна некорректен: не задан тенант или
	// кампания, либо курсор заполнен частично. Пустой тенант — ошибка, а не
	// признак «без фильтра»: запрос окна не несёт тенант из кампании, поэтому
	// тенант задаётся явно и обязателен.
	ErrInvalidDraftWindowQuery = errors.New("порты: некорректный запрос окна черновиков")
)

// DraftWindowRepository — CampaignRepository, дополненный загрузкой окна
// черновиков. Расширение аддитивно: тот, кому нужен только Save/FindByID,
// продолжает зависеть от CampaignRepository; пакетный путь зависит от
// DraftWindowRepository.
//
// Порт не знает о драйвере БД: сигнатуры оперируют доменными идентификаторами,
// а не ключами хранилища.
type DraftWindowRepository interface {
	CampaignRepository

	// LoadDraftWindow возвращает окно черновиков кампании, начиная строго после
	// курсора запроса, не длиннее лимита и в детерминированном порядке
	// (материал, сегмент, идентификатор черновика).
	//
	// Контракт:
	//   - запрос проверяется через DraftWindowQuery.Validate;
	//   - кампания должна принадлежать query.TenantID: окно чужого тенанта
	//     недоступно и даёт ту же ошибку, что отсутствие кампании, — чужой тенант
	//     не отличается от несуществующей кампании;
	//   - отсутствие кампании даёт ошибку с errors.Is(err, ErrCampaignNotFound);
	//   - порядок результатов задаёт CompareDraftKey, повторный вызов с тем же
	//     запросом возвращает тот же состав и порядок;
	//   - в окне не больше query.Limit черновиков; курсор запроса исключающий
	//     (строго после ключа), поэтому соседние страницы не пересекаются и не
	//     пропускают черновики;
	//   - DraftWindow.Next — ключ последнего черновика окна; при HasMore он
	//     передаётся в After следующего запроса, иначе он нулевой;
	//   - окно не выгружает кампанию целиком: возвращается только выбранный
	//     срез черновиков и курсор, а не агрегат.
	LoadDraftWindow(ctx context.Context, query DraftWindowQuery) (DraftWindow, error)
}

// DraftWindowQuery — запрос окна черновиков одной кампании.
type DraftWindowQuery struct {
	// TenantID — тенант-владелец кампании; обязателен. Запрос окна не выводит
	// тенант из кампании, поэтому тенант задаётся явно, а хранилище сверяет его
	// с кампанией. Пустой тенант отвергается, а не читается без фильтра.
	TenantID domain.TenantID

	// CampaignID — кампания-владелец; обязательна.
	CampaignID domain.CampaignID

	// After — исключающий курсор: окно начинается строго после этого ключа.
	// Нулевой курсор означает начало упорядоченной последовательности.
	After DraftWindowCursor

	// Limit — максимальное число черновиков в окне; должен быть положительным.
	Limit int
}

// DraftWindowCursor — ключ сортировки и позиция keyset-пагинации.
//
// Поля — существующие доменные идентификаторы, поэтому курсор не тянет типы
// хранилища. Нулевой курсор (все поля пусты) означает «с начала».
type DraftWindowCursor struct {
	Material domain.MaterialID
	Segment  domain.SegmentID
	DraftID  domain.DraftID
}

// IsZero сообщает, что курсор пуст и задаёт начало последовательности.
func (c DraftWindowCursor) IsZero() bool {
	return c.Material == "" && c.Segment == "" && c.DraftID == ""
}

// Compare задаёт детерминированный порядок окна: материал, затем сегмент, затем
// идентификатор черновика. Возвращает отрицательное число, если c меньше other,
// ноль при равенстве и положительное, если больше.
func (c DraftWindowCursor) Compare(other DraftWindowCursor) int {
	if result := strings.Compare(string(c.Material), string(other.Material)); result != 0 {
		return result
	}
	if result := strings.Compare(string(c.Segment), string(other.Segment)); result != 0 {
		return result
	}
	return strings.Compare(string(c.DraftID), string(other.DraftID))
}

// Validate проверяет, что курсор либо пуст, либо заполнен целиком.
func (c DraftWindowCursor) Validate() error {
	filled := 0
	if c.Material != "" {
		filled++
	}
	if c.Segment != "" {
		filled++
	}
	if c.DraftID != "" {
		filled++
	}
	if filled == 0 || filled == 3 {
		return nil
	}
	return fmt.Errorf("%w: курсор заполнен частично", ErrInvalidDraftWindowQuery)
}

// Validate проверяет запрос до обращения к хранилищу. Реализации вызывают её
// первой и возвращают ошибку как есть, чтобы отказ был одинаков у всех.
func (q DraftWindowQuery) Validate() error {
	if strings.TrimSpace(q.TenantID.String()) == "" {
		return fmt.Errorf("%w: тенант не задан", ErrInvalidDraftWindowQuery)
	}
	if strings.TrimSpace(q.CampaignID.String()) == "" {
		return fmt.Errorf("%w: кампания не задана", ErrInvalidDraftWindowQuery)
	}
	if q.Limit <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidDraftWindowLimit, q.Limit)
	}
	return q.After.Validate()
}

// CompareDraftKey сравнивает два ключа окна. Экспортируется, чтобы реализации и
// contract suite применяли один порядок без дублирования правил.
func CompareDraftKey(left, right DraftWindowCursor) int {
	return left.Compare(right)
}

// CursorOf возвращает ключ сортировки черновика. Реализации строят по нему
// keyset-позицию, suite — ожидаемый порядок.
func CursorOf(draft *domain.ActionDraft) DraftWindowCursor {
	spec := draft.Spec()
	return DraftWindowCursor{
		Material: spec.Material,
		Segment:  spec.Segment,
		DraftID:  draft.ID(),
	}
}

// DraftWindow — результат загрузки окна: только выбранные черновики и позиция
// продолжения, без агрегата кампании.
type DraftWindow struct {
	// Drafts — черновики окна в детерминированном порядке, не длиннее лимита.
	Drafts []*domain.ActionDraft

	// HasMore — в кампании есть черновики строго после Next.
	HasMore bool

	// Next — ключ последнего черновика окна для следующего запроса в After.
	// Нулевой, если HasMore ложен.
	Next DraftWindowCursor
}

// IsEmpty сообщает, что окно не содержит черновиков.
func (w DraftWindow) IsEmpty() bool {
	return len(w.Drafts) == 0
}
