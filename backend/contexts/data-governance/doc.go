// Package datagovernance собирает сервис контекста data-governance: соединение
// адаптеров, портов и use cases своего контекста и публикацию его ролей.
//
// Контекст отвечает за зоны данных, основание обработки, поручение клиента и
// правила подавления контактов (architecture/domain-catalog.yaml). Агрегат
// processing-governance и его политики описаны в internal/domain; публикуемые
// контракты — в модуле contracts/data-governance/datazonepolicy,
// processingauthority и suppressionpolicy, а их in-proc адаптеры — в contract.go.
//
// Пакет лежит в каталоге с дефисом (contexts/data-governance), потому что
// идентификатор сервиса равен идентификатору контекста из каталога, а имя
// Go-пакета должно быть валидным идентификатором.
//
// Это единственное место сборки своего контекста; бизнес-правил здесь нет.
package datagovernance
