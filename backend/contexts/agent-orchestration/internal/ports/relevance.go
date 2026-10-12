package ports

import (
	"context"
	"errors"
)

// Read-контракт lead-relevance владельца lead-management (design.md, Реш. 6):
// вердикт оценки релевантности компании в контексте задания. На момент реализации
// контракт ещё не опубликован, поэтому порт описывает минимальную форму, которой
// достаточно блоку classify; адаптер поверх опубликованного контракта заменит
// инъектируемую реализацию без изменения блока.
//
// Правило владения: оценка принадлежит lead-management и не является постоянным
// свойством компании — порт запрашивает вердикт для конкретного (тенант, задание,
// компания) и не переносит результат между заданиями.
var (
	// ErrRelevanceRejected — оценка отклонена владельцем по доменной причине
	// (например, неизвестная компания). Потребитель отличает это от сбоя транспорта.
	ErrRelevanceRejected = errors.New("relevance: оценка отклонена владельцем")
	// ErrRelevanceUnavailable — владелец оценки недоступен: контракт не подключён
	// или сбой транспорта. Потребитель не выдаёт это за вердикт.
	ErrRelevanceUnavailable = errors.New("relevance: владелец оценки недоступен")
)

// RelevanceVerdict — нормализованный вердикт оценки. Набор закрыт: блок не
// разбирает произвольные значения владельца, а сводит их к объявленным.
type RelevanceVerdict string

const (
	// RelevanceSuitable — компания подходит в контексте задания.
	RelevanceSuitable RelevanceVerdict = "suitable"
	// RelevanceUnsuitable — компания не подходит в контексте задания.
	RelevanceUnsuitable RelevanceVerdict = "unsuitable"
)

// RelevanceQuery — вход оценки: тенант, задание и компания. Содержания чужой
// карточки здесь нет — владелец читает её сам.
type RelevanceQuery struct {
	TenantID  string
	MissionID string
	CompanyID string
	// Criteria — оффер/цель задания, относительно которой оценивается релевантность.
	Criteria string
}

// RelevanceEvaluation — исход оценки.
type RelevanceEvaluation struct {
	// Verdict — вердикт в контексте задания.
	Verdict RelevanceVerdict
	// BasisRef — непрозрачная ссылка на свидетельства/обоснование оценки без
	// персональных данных. Пусто — владелец основания не раскрывает.
	BasisRef string
}

// RelevanceEvaluator — порт чтения вердикта оценки. Отсутствие реализации (nil)
// означает честную недоступность блока classify, а не выдуманный вердикт.
type RelevanceEvaluator interface {
	Evaluate(ctx context.Context, query RelevanceQuery) (RelevanceEvaluation, error)
}
