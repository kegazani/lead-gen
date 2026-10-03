package components

import (
	"bytes"
	"strings"
	"testing"
)

// TestFieldRequiredForTextarea проверяет, что обязательное поле-текстареа получает
// атрибут required так же, как однострочное: без него браузер отправит пустое
// значение, и обязательность останется только серверной.
func TestFieldRequiredForTextarea(t *testing.T) {
	var buffer bytes.Buffer
	err := renderFragment(&buffer, "field", Field{
		ID: "description", Name: "description", Label: "Описание",
		Type: "textarea", Rows: 5, Required: true,
	})
	if err != nil {
		t.Fatalf("рендер поля: %v", err)
	}
	if !strings.Contains(buffer.String(), "required") {
		t.Fatalf("обязательная текстареа без required:\n%s", buffer.String())
	}
}

// TestFieldOptionalTextareaHasNoRequired проверяет, что необязательное поле-
// текстареа required не получает: атрибут следует за моделью, а не типом.
func TestFieldOptionalTextareaHasNoRequired(t *testing.T) {
	var buffer bytes.Buffer
	err := renderFragment(&buffer, "field", Field{
		ID: "comment", Name: "comment", Label: "Комментарий", Type: "textarea",
	})
	if err != nil {
		t.Fatalf("рендер поля: %v", err)
	}
	if strings.Contains(buffer.String(), "required") {
		t.Fatalf("необязательная текстареа получила required:\n%s", buffer.String())
	}
}
