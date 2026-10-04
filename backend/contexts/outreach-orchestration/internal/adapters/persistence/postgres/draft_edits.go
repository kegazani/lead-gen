package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Кодирование записей правок черновика для колонки action_draft.edits.
//
// Домен хранит состав (DraftSpec) value object с закрытым именем канала
// (ActionChannel), поэтому напрямую encoding/json его сериализовать нельзя:
// имя канала потерялось бы молча. Запись правки раскладывается в явную
// проводимую форму со строковыми значениями, а при загрузке собирается заново
// через domain.ParseActionChannel, то есть проверки домена выполняются и на
// обратном пути. Неизвестный канал отвергается ошибкой, а не читается усечённым.

// draftEditWire — проводимая форма domain.DraftEdit.
type draftEditWire struct {
	Actor         string    `json:"actor"`
	At            time.Time `json:"at"`
	FromMaterial  string    `json:"from_material"`
	FromSegment   string    `json:"from_segment"`
	FromRecipient string    `json:"from_recipient"`
	FromChannel   string    `json:"from_channel"`
	ToMaterial    string    `json:"to_material"`
	ToSegment     string    `json:"to_segment"`
	ToRecipient   string    `json:"to_recipient"`
	ToChannel     string    `json:"to_channel"`
}

// encodeDraftEdits сериализует историю правок черновика в JSON. Пустая история
// даёт пустой массив, а не null: колонка NOT NULL.
func encodeDraftEdits(edits []domain.DraftEdit) ([]byte, error) {
	wire := make([]draftEditWire, 0, len(edits))
	for _, edit := range edits {
		wire = append(wire, draftEditWire{
			Actor:         edit.Actor.String(),
			At:            edit.At,
			FromMaterial:  string(edit.From.Material),
			FromSegment:   string(edit.From.Segment),
			FromRecipient: edit.From.Recipient.String(),
			FromChannel:   edit.From.Channel.String(),
			ToMaterial:    string(edit.To.Material),
			ToSegment:     string(edit.To.Segment),
			ToRecipient:   edit.To.Recipient.String(),
			ToChannel:     edit.To.Channel.String(),
		})
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("postgres: сериализация правок черновика: %w", err)
	}
	return payload, nil
}

// decodeDraftEdits восстанавливает историю правок из JSON. Каналы обеих сторон
// разбираются доменным конструктором: неизвестный канал — ошибка чтения.
func decodeDraftEdits(payload []byte) ([]domain.DraftEdit, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var wire []draftEditWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, fmt.Errorf("postgres: разбор правок черновика: %w", err)
	}
	edits := make([]domain.DraftEdit, 0, len(wire))
	for _, item := range wire {
		from, err := decodeDraftSpec(item.FromMaterial, item.FromSegment, item.FromRecipient, item.FromChannel)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор прежнего состава правки: %w", err)
		}
		to, err := decodeDraftSpec(item.ToMaterial, item.ToSegment, item.ToRecipient, item.ToChannel)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор нового состава правки: %w", err)
		}
		edits = append(edits, domain.DraftEdit{
			Actor: domain.ActorID(item.Actor),
			At:    item.At,
			From:  from,
			To:    to,
		})
	}
	return edits, nil
}

// decodeDraftSpec собирает состав из проводимых строк, разбирая канал доменным
// конструктором.
func decodeDraftSpec(material, segment, recipient, channel string) (domain.DraftSpec, error) {
	parsed, err := domain.ParseActionChannel(channel)
	if err != nil {
		return domain.DraftSpec{}, err
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID(material),
		Segment:   domain.SegmentID(segment),
		Recipient: domain.RecipientID(recipient),
		Channel:   parsed,
	}, nil
}
