package ports

import (
	"context"
	"errors"
)

// ErrArtifactUnavailable — статус артефакта фулфилмента недоступен: контракт
// владельца не подключён или вернул отказ. Это отказ чтения, а не «артефакта
// нет»: последнее выражается полем Found в ArtifactStatus.
var ErrArtifactUnavailable = errors.New("порты: статус артефакта фулфилмента недоступен")

// ArtifactKind — вид артефакта фулфилмента, на который ссылается заявка.
type ArtifactKind string

const (
	// ArtifactAudienceSnapshot — снимок аудитории (владение audience).
	ArtifactAudienceSnapshot ArtifactKind = "audience-snapshot"

	// ArtifactCampaign — кампания (владение outreach-orchestration).
	ArtifactCampaign ArtifactKind = "campaign"

	// ArtifactDeal — сделка (владение sales-workflow).
	ArtifactDeal ArtifactKind = "deal"
)

// ArtifactRef — ссылка на артефакт фулфилмента по виду и идентификатору.
type ArtifactRef struct {
	Kind ArtifactKind
	ID   string
}

// ArtifactStatus — прочитанный статус артефакта. Found равно false, если
// владелец подтвердил отсутствие артефакта по идентификатору.
type ArtifactStatus struct {
	Ref    ArtifactRef
	Status string
	Found  bool
}

// FulfillmentQuery — синхронный query-контракт: чтение статуса артефакта
// фулфилмента у контекста-владельца по идентификатору. Транспорт контракта —
// sync/http-json; заявка не импортирует модель владельца, а получает лишь статус.
type FulfillmentQuery interface {
	// Status возвращает статус артефакта. Отказ чтения — ошибка, для которой
	// истинно errors.Is(err, ErrArtifactUnavailable).
	Status(ctx context.Context, ref ArtifactRef) (ArtifactStatus, error)
}
