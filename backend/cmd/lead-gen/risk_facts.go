package main

import (
	"strings"

	datagovernance "lead-gen/backend/contexts/data-governance"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	addressverificationhttp "lead-gen/contracts/lead-management/addressverification/httpport"
	deliverytargethttp "lead-gen/contracts/lead-management/deliverytarget/httpport"
)

// assembleRiskFacts собирает сборщик признаков риска пакетного пути
// outreach-orchestration (capability risk-screening) поверх портов владельцев:
//
//   - адрес получателя — контракт delivery-target (lead-management): сетевой
//     клиент при заданном адресе сервиса, иначе in-proc резолвер владельца при
//     co-location;
//   - статус верификации адреса — контракт address-verification (lead-management):
//     in-proc адаптер поверх того же хранилища контактов; сетевой транспорт —
//     точка расширения (design.md, «Отложено»);
//   - стоп-лист — контракт suppression-policy (data-governance): реальный срез по
//     DSN зелёной зоны; без DSN источник недоступен.
//
// Незаданный владелец не подменяется успехом: сборщик честно отказывает при
// обращении, поэтому пакетное утверждение при неполном контуре честно недоступно,
// а не выдаётся как «рисков нет» (AGENTS.md §3). Значение кладётся в
// opts.riskFacts и используется сборкой сервиса outreach-orchestration.
func assembleRiskFacts(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	// Потребитель — только outreach-orchestration: без него сборщик не собирается,
	// чтобы не тянуть владельцев ради неработающего потребителя.
	if _, ok := findSelected(selected, outreachorchestration.ServiceID); !ok {
		return
	}

	ports := outreachorchestration.RiskFactsPorts{}

	// Владелец получателя и верификации — lead-management. Хранилище контактов
	// берётся у собранного сервиса-владельца (то же, что использует сам сервис),
	// а не собирается вторым экземпляром.
	if repos, ready := leadRepositoriesForRisk(selected, opts, buildOnce, logger); ready {
		if strings.TrimSpace(opts.deliveryTargetURL) == "" {
			ports.Recipients = leadmanagement.NewDeliveryTarget(leadmanagement.DeliveryTargetDeps{
				Repositories: repos,
				TenantID:     opts.tenant,
			})
		}
		ports.Verification = leadmanagement.NewAddressVerification(leadmanagement.AddressVerificationDeps{
			Repositories:     repos,
			TenantID:         opts.tenant,
			VerifierEndpoint: opts.addressVerifierURL,
			VerifierAPIKey:   opts.addressVerifierKey,
		})
	}

	// Сетевой резолвер получателя: при разделении сервисов адрес контракта задаёт
	// точка сборки, и владелец в этом процессе не собирается.
	if ports.Recipients == nil && strings.TrimSpace(opts.deliveryTargetURL) != "" {
		client, err := deliverytargethttp.New(opts.deliveryTargetURL)
		if err != nil {
			logger.Error("lead-gen: риск-скрининг — сетевой клиент delivery-target", err, observ.Fields{})
		} else {
			ports.Recipients = client
		}
	}

	// Сетевой адаптер верификации адреса: при разделении сервисов владелец
	// (lead-management) в другом процессе. Незаданный адрес оставляет порт пустым
	// (честная недоступность), а не подменяет проверку выдуманным «подтверждён».
	if ports.Verification == nil && strings.TrimSpace(opts.addressVerificationURL) != "" {
		client, err := addressverificationhttp.New(opts.addressVerificationURL)
		if err != nil {
			logger.Error("lead-gen: риск-скрининг — сетевой клиент address-verification", err, observ.Fields{})
		} else {
			ports.Verification = client
		}
	}

	// Стоп-лист — контракт suppression-policy владельца data-governance: реальный
	// срез по DSN зелёной зоны. Без DSN реального источника нет — порт остаётся
	// незаданным, и сборщик честно отказывает.
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		govSlice, err := datagovernance.Build(datagovernance.Config{DSN: dsn, Logger: opts.logger})
		if err != nil {
			logger.Error("lead-gen: риск-скрининг — стоп-лист data-governance", err, observ.Fields{})
		} else {
			ports.Suppression = datagovernance.NewSuppressionPolicy(govSlice)
		}
	}

	var missing []string
	if ports.Recipients == nil {
		missing = append(missing, "delivery-target")
	}
	if ports.Suppression == nil {
		missing = append(missing, "suppression-policy")
	}
	if ports.Verification == nil {
		missing = append(missing, "address-verification")
	}

	opts.riskFacts = outreachorchestration.NewRiskFacts(ports)
	if len(missing) > 0 {
		logger.Warn("lead-gen: риск-скрининг неполон — пакетное утверждение честно недоступно; нет владельцев: "+strings.Join(missing, ", "), observ.Fields{})
		return
	}
	logger.Info("lead-gen: риск-скрининг подключён (risk-screening)", observ.Fields{})
}

// leadRepositoriesForRisk собирает сервис-владелец lead-management (если он выбран
// и ещё не собран) и возвращает его адаптеры хранилища. Владелец не выбран или его
// хранилище недоступно — false: контракты получают честную недоступность.
func leadRepositoriesForRisk(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) (leadmanagement.Repositories, bool) {
	lm, ok := findSelected(selected, leadmanagement.ServiceID)
	if !ok {
		return leadmanagement.Repositories{}, false
	}
	if _, err := buildOnce(lm); err != nil {
		logger.Error("lead-gen: риск-скрининг — сборка владельца lead-management", err, observ.Fields{})
		return leadmanagement.Repositories{}, false
	}
	repos, ready := opts.leadStore.load()
	if !ready {
		logger.Warn("lead-gen: риск-скрининг — хранилище lead-management не собрано", observ.Fields{})
	}
	return repos, ready
}
