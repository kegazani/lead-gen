# archlint

Проверяет канонический каталог доменной архитектуры, его соответствие JSON Schema, владение сущностями, покрытие OpenSpec capability, направленные связи и обязательные PlantUML-диаграммы.

Запуск из корня репозитория:

```bash
go -C tools/archlint run .
```

Unit-тесты:

```bash
go -C tools/archlint test ./...
```

Сетевой доступ и внешние Go-модули не требуются. `domain-catalog.yaml` использует JSON-синтаксис, допустимый в YAML 1.2.
