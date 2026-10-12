package domain

import (
	"fmt"
	"strings"
)

// Block — value object, описывающий зарегистрированный блок обработки.
//
// Блок — именованный элемент обработки (search, classify, enrich, verify-email,
// size-assess, generate-email, await-approval, send, observe, decide-next).
// Регистрация блока и его привязка к контракту владельца данных — данные реестра,
// а не ветвление в коде супервизора (design.md, Реш. 3). Незнакомый блок не
// исполняется и не подменяется другим.
type Block struct {
	ref          BlockRef
	name         string
	contract     string
	irreversible bool
	mandatory    bool
}

// NewBlock собирает описание блока. Ссылка обязательна; имя и контракт
// нормализуются.
func NewBlock(ref BlockRef, name, contract string, irreversible, mandatory bool) (Block, error) {
	if err := ref.validate(); err != nil {
		return Block{}, err
	}
	return Block{
		ref:          ref,
		name:         strings.TrimSpace(name),
		contract:     strings.TrimSpace(contract),
		irreversible: irreversible,
		mandatory:    mandatory,
	}, nil
}

// Ref возвращает ссылку блока.
func (b Block) Ref() BlockRef { return b.ref }

// Name возвращает человекочитаемое имя блока.
func (b Block) Name() string { return b.name }

// Contract возвращает имя контракта владельца, через который исполняется блок.
func (b Block) Contract() string { return b.contract }

// Irreversible сообщает, что блок выполняет необратимое действие (отправку):
// такой шаг требует положительного решения оператора по точной версии черновика
// (policy irreversible-step-approval).
func (b Block) Irreversible() bool { return b.irreversible }

// Mandatory сообщает, что блок обязателен: его недоступность держит обработку
// компании незавершённой, а не закрывает её успехом.
func (b Block) Mandatory() bool { return b.mandatory }

// validate проверяет описание блока.
func (b Block) validate() error {
	if err := b.ref.validate(); err != nil {
		return fmt.Errorf("блок: %w", err)
	}
	return nil
}

// BlockRegistry — реестр зарегистрированных блоков (policy
// extensible-block-registry). Реестр — данные, а не код оркестратора: состав
// блоков расширяется регистрацией, супервизор при этом не меняется.
//
// Реестр неизменяем после сборки: Lookup только читает.
type BlockRegistry struct {
	blocks map[BlockRef]Block
	order  []BlockRef
}

// NewBlockRegistry собирает реестр из описаний блоков. Дублирующаяся ссылка —
// ошибка: две трактовки одного блока недопустимы.
func NewBlockRegistry(blocks ...Block) (*BlockRegistry, error) {
	registry := &BlockRegistry{blocks: make(map[BlockRef]Block, len(blocks))}
	for _, block := range blocks {
		if err := block.validate(); err != nil {
			return nil, err
		}
		if _, exists := registry.blocks[block.ref]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateBlock, block.ref)
		}
		registry.blocks[block.ref] = block
		registry.order = append(registry.order, block.ref)
	}
	return registry, nil
}

// Lookup возвращает описание блока по ссылке.
func (r *BlockRegistry) Lookup(ref BlockRef) (Block, bool) {
	if r == nil {
		return Block{}, false
	}
	block, ok := r.blocks[ref]
	return block, ok
}

// Registered сообщает, что блок зарегистрирован.
func (r *BlockRegistry) Registered(ref BlockRef) bool {
	_, ok := r.Lookup(ref)
	return ok
}

// Blocks возвращает описания блоков в порядке регистрации.
func (r *BlockRegistry) Blocks() []Block {
	if r == nil {
		return nil
	}
	out := make([]Block, 0, len(r.order))
	for _, ref := range r.order {
		out = append(out, r.blocks[ref])
	}
	return out
}
