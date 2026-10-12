package ports

// PasswordHasher — порт хеширования и проверки паролей клиента. Домен хранит
// только хеш; сырой пароль в домен не попадает. Формат хеша задаёт адаптер.
type PasswordHasher interface {
	// Hash возвращает необратимый хеш пароля (с солью).
	Hash(plain string) (string, error)

	// Verify сообщает, соответствует ли пароль сохранённому хешу.
	Verify(plain, hashed string) bool
}
