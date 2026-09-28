package domain

import (
	"fmt"
	"time"
)

// AdapterRegistration — aggregate root adapter-registration: факт регистрации
// адаптера с объявленным manifest. Регистрация неизменяема после создания:
// повторная регистрация того же имени — отказ, а не замена.
type AdapterRegistration struct {
	id       AdapterID
	manifest Manifest
	events   []DomainEvent
}

// RegisterAdapterParams — данные команды RegisterAdapter.
type RegisterAdapterParams struct {
	// ID — нейтральный внутренний идентификатор регистрации. Обязателен.
	ID AdapterID
	// Manifest — объявленный manifest адаптера. Обязателен и валиден.
	Manifest Manifest
	// RegisteredAt — момент регистрации. Обязателен.
	RegisteredAt time.Time
}

// RegisterAdapter создаёт регистрацию адаптера и эмитит adapter-registered.
//
// Регистрация с пустым manifest не создаётся: имя, версия и набор возможностей
// проверяются manifest'ом. Событие несёт только идентификатор, имя, версию и
// возможности — секретов в нём нет.
func RegisterAdapter(params RegisterAdapterParams) (*AdapterRegistration, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if params.Manifest.IsZero() {
		return nil, fmt.Errorf("%w: регистрация %s", ErrEmptyManifestCapabilities, params.ID)
	}
	if err := params.Manifest.validate(); err != nil {
		return nil, err
	}
	if params.RegisteredAt.IsZero() {
		return nil, fmt.Errorf("%w: регистрация %s", ErrInvalidSnapshot, params.ID)
	}

	registration := &AdapterRegistration{
		id:       params.ID,
		manifest: params.Manifest,
	}
	emit(&registration.events, AdapterRegistered{
		AdapterID:       registration.id,
		AdapterName:     registration.manifest.AdapterName(),
		ManifestVersion: registration.manifest.Version(),
		Capabilities:    registration.manifest.Capabilities(),
		At:              params.RegisteredAt,
	})
	return registration, nil
}

// ID возвращает нейтральный идентификатор регистрации.
func (r *AdapterRegistration) ID() AdapterID { return r.id }

// Name возвращает стабильное имя адаптера.
func (r *AdapterRegistration) Name() AdapterName { return r.manifest.AdapterName() }

// Manifest возвращает объявленный manifest.
func (r *AdapterRegistration) Manifest() Manifest { return r.manifest }

// Declares сообщает, объявлена ли возможность в manifest регистрации.
func (r *AdapterRegistration) Declares(capability CapabilityName) bool {
	return r.manifest.Declares(capability)
}

// Require проверяет объявленную возможность. Инвариант «возможности адаптера
// проверяются до вызова» живёт в агрегате, поэтому любой вызывающий путь проходит
// через эту проверку.
func (r *AdapterRegistration) Require(capability CapabilityName) error {
	return r.manifest.Require(capability)
}

// DomainEvents возвращает копию неподтверждённых событий.
func (r *AdapterRegistration) DomainEvents() []DomainEvent { return copyEvents(r.events) }

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (r *AdapterRegistration) PullDomainEvents() []DomainEvent { return drainEvents(&r.events) }

// Проверка на этапе компиляции: агрегат публикует свои события.
var _ EventProducer = (*AdapterRegistration)(nil)
