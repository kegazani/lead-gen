package blocks

import (
	"strconv"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// materialSeparator разделяет ссылку на материал письма на идентификатор пакета и
// версию (asset@version). Формат непрозрачен для владельца материала и нужен лишь
// агенту, чтобы связать решение оператора с точной версией черновика.
const materialSeparator = "@"

// stepIdempotencyKey выводит ключ идемпотентности шага из запуска и шага пайплайна.
// Повтор шага адресует тот же внешний запрос и не создаёт второй эффект.
func stepIdempotencyKey(runID, stepID string) string {
	return runID + ":" + stepID
}

// buildMaterialRef собирает непрозрачную ссылку на материал письма.
func buildMaterialRef(assetID, versionID string) string {
	return strings.TrimSpace(assetID) + materialSeparator + strings.TrimSpace(versionID)
}

// splitMaterialRef разбирает ссылку на материал на ссылку черновика и
// целочисленную версию домена. Ссылка черновика остаётся непрозрачной (совпадает с
// ссылкой материала); числовая версия извлекается из идентификатора версии. Если
// числовой версии нет, версия считается первой.
func splitMaterialRef(ref string) (domain.DraftRef, int) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", 0
	}
	if idx := strings.LastIndex(trimmed, materialSeparator); idx >= 0 {
		return domain.DraftRef(trimmed), versionFromID(trimmed[idx+1:])
	}
	return domain.DraftRef(trimmed), versionFromID(trimmed)
}

// versionFromID извлекает первую группу цифр из идентификатора версии. Числовая
// версия нужна домену для привязки решения оператора; при её отсутствии берётся 1.
func versionFromID(id string) int {
	digits := strings.Builder{}
	for _, r := range strings.TrimSpace(id) {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 1
	}
	value, err := strconv.Atoi(digits.String())
	if err != nil || value <= 0 {
		return 1
	}
	return value
}
