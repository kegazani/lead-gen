package domain

import "fmt"

// Offer — entity внутри aggregate request: описание того, что клиент предлагает
// и кому (VO offer-content) и цель заявки (VO request-target).
//
// Оффер версионируется полем revision. Правка не меняет существующую версию на
// месте: агрегат создаёт новую версию с revision+1, а прежняя остаётся
// неизменной. До валидации это защищает историю правок, после validated —
// выполняет инвариант «оффер не редактируется на месте».
type Offer struct {
	id        OfferID
	content   OfferContent
	target    RequestTarget
	revision  int
	embedding []float32
}

// newOffer создаёт первую версию оффера.
func newOffer(id OfferID, content OfferContent, target RequestTarget) (*Offer, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := content.checkVersioned(); err != nil {
		return nil, fmt.Errorf("%w: оффер %s", err, id)
	}
	return &Offer{id: id, content: content, target: target, revision: 1}, nil
}

// ID возвращает идентификатор оффера.
func (o *Offer) ID() OfferID { return o.id }

// Content возвращает текст оффера.
func (o *Offer) Content() OfferContent { return o.content }

// Target возвращает цель заявки.
func (o *Offer) Target() RequestTarget { return o.target }

// Revision возвращает номер версии оффера.
func (o *Offer) Revision() int { return o.revision }

// Embedding возвращает вектор оффера (nil — не сформирован).
func (o *Offer) Embedding() []float32 { return append([]float32(nil), o.embedding...) }

// SetEmbedding фиксирует вектор оффера. Пустой вектор отвергается: эмбеддинг без
// вектора не имеет смысла.
func (o *Offer) SetEmbedding(vector []float32) error {
	if len(vector) == 0 {
		return fmt.Errorf("%w: оффер %s", ErrEmptyEmbedding, o.id)
	}
	o.embedding = append([]float32(nil), vector...)
	return nil
}

// newRevision создаёт новую версию оффера на основе текущей. Прежняя версия не
// изменяется: вызывающий сохраняет её в истории агрегата.
func (o *Offer) newRevision(id OfferID, content OfferContent, target RequestTarget) (*Offer, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := content.checkVersioned(); err != nil {
		return nil, fmt.Errorf("%w: оффер %s", err, id)
	}
	return &Offer{
		id:       id,
		content:  content,
		target:   target,
		revision: o.revision + 1,
	}, nil
}

// edit меняет текущую версию оффера на месте, сохраняя идентификатор и ревизию.
// Применяется до validated: валидатор уточняет формулировки, и версия оффера не
// растёт (правка «на месте» из capability request-validation). Вектор сбрасывается:
// он посчитан из прежнего текста и после правки устарел.
func (o *Offer) edit(content OfferContent, target RequestTarget) error {
	if err := content.checkVersioned(); err != nil {
		return fmt.Errorf("%w: оффер %s", err, o.id)
	}
	o.content = content
	o.target = target
	o.embedding = nil
	return nil
}

// Snapshot возвращает состояние оффера для сохранения.
func (o *Offer) Snapshot() OfferSnapshot {
	return OfferSnapshot{
		ID:        o.id,
		Content:   o.content,
		Target:    o.target,
		Revision:  o.revision,
		Embedding: append([]float32(nil), o.embedding...),
	}
}

// rehydrateOffer восстанавливает оффер из сохранённого состояния, проверяя
// инварианты. События не эмитятся: восстановление — не новый факт домена.
func rehydrateOffer(s OfferSnapshot) (*Offer, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.Content.checkVersioned(); err != nil {
		return nil, fmt.Errorf("%w: оффер %s", err, s.ID)
	}
	if s.Revision <= 0 {
		return nil, fmt.Errorf("%w: оффер %s версии %d", ErrInvalidOfferRevision, s.ID, s.Revision)
	}
	return &Offer{
		id:        s.ID,
		content:   s.Content,
		target:    s.Target,
		revision:  s.Revision,
		embedding: append([]float32(nil), s.Embedding...),
	}, nil
}
