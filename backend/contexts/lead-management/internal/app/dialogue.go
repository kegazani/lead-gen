package app

import (
	"context"
	"errors"
	"iter"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/llm"
)

// defaultMaxDialogueTurns — предел числа уточняющих ходов диалога по умолчанию.
const defaultMaxDialogueTurns = 8

// dialogueLimitMessage — реплика при достижении предела уточнений.
const dialogueLimitMessage = "Достигнут предел уточнений. Сформулируйте условия короче или запустите поиск по текущему плану."

// errDialogueUnavailable — LLM не настроен, диалог вести нечем.
var errDialogueUnavailable = errors.New("app: диалог недоступен: LLM не настроен")

// ConverseDialogue — use case диалогового подбора стратегии поиска: один ход
// поверх llm.Port.Converse с предохранителем длины диалога. История приходит от
// клиента, сервер её не хранит.
type ConverseDialogue struct {
	// LLM — порт LLM. Обязателен: без него диалог невозможен.
	LLM llm.Port
	// MaxTurns — предел числа ходов оператора (0 — значение по умолчанию).
	MaxTurns int
	// Logger — безопасный логгер ходов (без текста реплик). nil — без логов.
	Logger observ.Logger
}

// Reply выполняет один ход диалога: проверяет историю, соблюдает предел ходов и
// возвращает реплику агента с черновиком целей. При достижении предела новые
// вопросы не задаются — возвращается сообщение о пределе.
func (d *ConverseDialogue) Reply(ctx context.Context, history []llm.Message) (llm.ConverseReply, error) {
	request := llm.ConverseRequest{History: history}
	if err := request.Validate(); err != nil {
		return llm.ConverseReply{}, err
	}
	if d.LLM == nil {
		return llm.ConverseReply{}, errDialogueUnavailable
	}
	turns := userTurns(history)
	if turns > d.limit() {
		d.log("предел ходов диалога достигнут", "turns", turns, "limit", d.limit())
		return llm.ConverseReply{Text: dialogueLimitMessage}, nil
	}

	reply, usage, err := d.LLM.Converse(ctx, request)
	if err != nil {
		return llm.ConverseReply{}, err
	}
	d.log("ход диалога", "turns", turns, "drafts", len(reply.Targets), "ready", reply.Ready, "tokens", usage.TotalTokens)
	return reply, nil
}

// StreamReply выполняет ход диалога с потоковой отдачей реплики: проверяет
// историю, соблюдает предел ходов и транслирует поток порта LLM, не накапливая
// текст. Ошибка валидации, недоступность LLM или предел ходов отдаются
// единственным кадром — завершающим, — чтобы клиентская лента всегда получала
// конец хода, а предел не порождал новых вопросов.
func (d *ConverseDialogue) StreamReply(ctx context.Context, history []llm.Message) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {
		request := llm.ConverseRequest{History: history}
		if err := request.Validate(); err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		if d.LLM == nil {
			yield(llm.ConverseDelta{}, errDialogueUnavailable)
			return
		}

		turns := userTurns(history)
		if turns > d.limit() {
			d.log("предел ходов диалога достигнут", "turns", turns, "limit", d.limit())
			reply := llm.ConverseReply{Text: dialogueLimitMessage}
			yield(llm.ConverseDelta{Reply: &reply}, nil)
			return
		}

		gotFinal := false
		for delta, err := range d.LLM.ConverseStream(ctx, request) {
			if err != nil {
				yield(llm.ConverseDelta{}, err)
				return
			}
			if delta.Reply != nil {
				gotFinal = true
				d.log("ход диалога (поток)", "turns", turns, "drafts", len(delta.Reply.Targets), "ready", delta.Reply.Ready, "tokens", delta.Usage.TotalTokens)
			}
			if !yield(delta, nil) {
				return
			}
		}
		if !gotFinal {
			yield(llm.ConverseDelta{}, errors.New("app: поток диалога завершился без ответа"))
		}
	}
}

// userTurns считает ходы оператора в истории.
func userTurns(history []llm.Message) int {
	n := 0
	for _, m := range history {
		if m.Role == llm.RoleUser {
			n++
		}
	}
	return n
}

// limit возвращает действующий предел числа ходов.
func (d *ConverseDialogue) limit() int {
	if d.MaxTurns > 0 {
		return d.MaxTurns
	}
	return defaultMaxDialogueTurns
}

// log пишет безопасные поля хода; текст реплик в логи не попадает.
func (d *ConverseDialogue) log(message string, attrs ...any) {
	if d.Logger == nil {
		return
	}
	d.Logger.Info(message, observ.Fields{}, attrs...)
}
