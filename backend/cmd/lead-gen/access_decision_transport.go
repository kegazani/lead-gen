package main

import (
	"fmt"

	"lead-gen/contracts/platform-governance/accessdecision"
	accessdecisionhttp "lead-gen/contracts/platform-governance/accessdecision/httpport"
)

// accessDecisionNetworkPort собирает сетевой клиент контракта access-decision к
// HTTP-поверхности владельца platform-governance. Возвращаемое значение — форма
// контракта (accessdecision.Decider), поэтому выбор транспорта по топологии
// остаётся за точкой входа, а код потребителей не меняется.
func accessDecisionNetworkPort(baseURL string) (accessdecision.Decider, error) {
	client, err := accessdecisionhttp.New(baseURL)
	if err != nil {
		return nil, fmt.Errorf("решение о доступе: сетевой клиент: %w", err)
	}
	return client, nil
}
