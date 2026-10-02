package ports

import "errors"

// ErrVectorUnavailable — векторное хранилище или эмбеддинг-сервис недоступны.
// Эмбеддинг оффера не формируется, состояние заявки не меняется.
var ErrVectorUnavailable = errors.New("эмбеддинг: векторное хранилище недоступно")
