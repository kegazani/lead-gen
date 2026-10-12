package http

// Отрицательная фикстура: HTTP-адаптер консоли контекста alpha подтягивает
// внутренний слой (ports) чужого контекста beta, минуя lead-gen/contracts/beta.
import _ "lead-gen/backend/contexts/beta/internal/ports"
