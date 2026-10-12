package mail

import "testing"

func TestMessageValidate(t *testing.T) {
	cases := []struct {
		name    string
		message Message
		wantErr bool
	}{
		{name: "валидное", message: Message{MessageID: "message-1", From: "a@example.com", To: "b@example.com", Subject: "тема", Body: "тело"}},
		{name: "без отправителя", message: Message{To: "b@example.com"}, wantErr: true},
		{name: "без получателя", message: Message{From: "a@example.com"}, wantErr: true},
		{name: "без идентификатора", message: Message{From: "a@example.com", To: "b@example.com"}, wantErr: true},
		{name: "пустые оба", message: Message{}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.message.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка валидации для %+v", tc.message)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}
