package http

// Отрицательная фикстура: HTTP-адаптер консоли контекста alpha импортирует
// app чужого контекста beta напрямую, минуя lead-gen/contracts/beta.
import _ "lead-gen/backend/contexts/beta/internal/app"
