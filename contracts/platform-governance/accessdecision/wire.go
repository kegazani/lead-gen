package accessdecision

// Wire-форма синхронного решения контракта. Путь и DTO объявлены в контракте,
// поэтому владелец (platform-governance) и сетевой клиент потребителей не
// расходятся в формате.

// PathCanAct — путь HTTP-поверхности владельца для синхронной проверки решения.
const PathCanAct = "/access-decision"

// CanActRequest — тело запроса решения: вправе ли актор действовать в тенанте.
type CanActRequest struct {
	// Actor — пользователь тенанта.
	Actor string `json:"actor"`
	// Tenant — тенант, в котором проверяется право.
	Tenant string `json:"tenant"`
}

// CanActResponse — ответ владельца на запрос решения.
type CanActResponse struct {
	// Allowed — положительное решение владельца.
	Allowed bool `json:"allowed"`
}
