package http

// Отрицательная фикстура: HTTP-адаптер консоли контекста alpha импортирует
// domain чужого контекста beta напрямую, минуя lead-gen/contracts/beta.
import _ "lead-gen/backend/contexts/beta/internal/domain"
