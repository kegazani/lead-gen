package domain

import "strings"

// ProfileStatus — состояние профиля компании. Профиль описывает, что компания
// предлагает и кого ищет; он становится активным только после согласования
// администратором.
type ProfileStatus string

const (
	// ProfileEmpty — профиль ещё не заполнен.
	ProfileEmpty ProfileStatus = "empty"
	// ProfilePending — профиль отправлен на согласование; правка запрещена.
	ProfilePending ProfileStatus = "pending"
	// ProfileActive — профиль подтверждён; клиент может подавать заявки.
	ProfileActive ProfileStatus = "active"
	// ProfileRejected — профиль отклонён; доступен для правки и повторной отправки.
	ProfileRejected ProfileStatus = "rejected"
)

// isValid сообщает, что состояние профиля объявлено доменом.
func (s ProfileStatus) isValid() bool {
	switch s {
	case ProfileEmpty, ProfilePending, ProfileActive, ProfileRejected:
		return true
	default:
		return false
	}
}

// Service — услуга компании в профиле. У услуги есть краткое описание для
// свёрнутого блока и полное — для развёрнутого.
type Service struct {
	Name  string
	Short string
	Full  string
}

// Profile — профиль компании: описание, целевая аудитория и перечень услуг.
type Profile struct {
	Description string
	Target      string
	Services    []Service
}

// NewProfile собирает профиль, нормализуя текст.
func NewProfile(description, target string, services []Service) Profile {
	normalized := make([]Service, 0, len(services))
	for _, service := range services {
		normalized = append(normalized, Service{
			Name:  strings.TrimSpace(service.Name),
			Short: strings.TrimSpace(service.Short),
			Full:  strings.TrimSpace(service.Full),
		})
	}
	return Profile{
		Description: strings.TrimSpace(description),
		Target:      strings.TrimSpace(target),
		Services:    normalized,
	}
}

// Complete сообщает, что профиль заполнен полностью: описание, целевая
// аудитория и хотя бы одна услуга с кратким и полным описанием.
func (p Profile) Complete() bool {
	if p.Description == "" || p.Target == "" || len(p.Services) == 0 {
		return false
	}
	for _, service := range p.Services {
		if service.Name == "" || service.Short == "" || service.Full == "" {
			return false
		}
	}
	return true
}
