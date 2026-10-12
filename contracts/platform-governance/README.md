# Контракты `platform-governance`

Пакеты этого Go-модуля — published language bounded context `platform-governance`.
Зависимости ограничены стандартной библиотекой; контракты не импортируют домены
владельца или потребителей.

- `accessdecision` — versioned policy snapshot и синхронный порт решения доступа.
- `tenantcontext` — обязательная граница tenant isolation для команды/сообщения.
- `auditrecorded` — неизменяемый факт аудита с корректировкой через новую запись.
- `tenantaccesschanged` — событие изменения статуса тенанта или набора ролей.

Сетевые envelope используют стабильный `event_id`; повторная доставка допустима,
потребитель обязан дедуплицировать сообщение.
