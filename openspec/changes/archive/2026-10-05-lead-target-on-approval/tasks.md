## 1. Экран валидации и проброс цели

- [x] 1.1 В форме одобрения экрана валидации (`validation_screen.html`) добавить необязательное числовое поле «Цель по числу лидов» (`lead_target`, `type=number`, `min=0`).
- [x] 1.2 В модели экрана (`screenView`/`screenModel`) завести конфигурацию поля цели и подпись.
- [x] 1.3 В `handleValidationApprove` разобрать `lead_target` и пробросить значение в `startFulfillment` вместо `0`.
- [x] 1.4 Изменить сигнатуру `startFulfillment`, чтобы принимать цель и передавать её в `ports.StartFulfillment.LeadTarget`.

## 2. Проверка цели

- [x] 2.1 `parseLeadTarget`: пусто → `0`; нецелое/отрицательное → `errInvalidLeadTarget`.
- [x] 2.2 `statusForError`: `errInvalidLeadTarget` → `400`; убедиться, что ошибка показывается на экране и одобрение не применяется.
- [x] 2.3 `handleStartWorkflow`: отвергнуть отрицательный `lead_target` тем же `400` для согласованности UI и API.

## 3. Тесты

- [x] 3.1 HTTP-тест: пустая цель → `LeadTarget == 0` (прежнее поведение).
- [x] 3.2 HTTP-тест: цель `N > 0` → `LeadTarget == N`.
- [x] 3.3 HTTP-тест: отрицательная цель → `400`, заявка остаётся `validating`, исполнение не запускается.
- [x] 3.4 HTTP-тест API: отрицательный `lead_target` в `start-workflow` → `400`.
- [x] 3.5 Золотой файл экрана валидации (`validation-screen.golden`) штатной перегенерацией с флагом `-update`.

## 4. Гейт

- [x] 4.1 `gofmt -l . && go build ./... && go vet ./... && go test ./...` в `backend/contexts/request-fulfillment` — зелёный.
- [x] 4.2 `openspec validate lead-target-on-approval --strict` — valid.
- [x] 4.3 На пути задачи нет заглушек (`AGENTS.md` §3).
