// Package domain содержит доменную модель bounded context
// community-intelligence: агрегат community-digest, его value objects
// (community-source, digest-period), доменные события и чистые политики
// обезличивания (authorized-community-read, digest-anonymization).
//
// Пакет не импортирует app, ports, adapters и другие контексты. В модель не
// попадают авторы сообщений и состав участников: это данные красной зоны
// (design.md, Решение 3). Агрегат и его value objects структурно не имеют полей
// для личности автора, поэтому обезличивание не может быть «забыто» на уровне
// типа.
package domain
