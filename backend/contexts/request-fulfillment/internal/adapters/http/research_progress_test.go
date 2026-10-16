package httpapi

import (
	"os/exec"
	"testing"
)

// Тест исполняет клиентский код, а не ищет в нём ожидаемые строки.
func TestResearchProgressBrowserState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не установлен: клиентский автомат проверяется в JS окружении")
	}
	output, err := exec.Command(node, "--test", "testdata/research-progress.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("клиентский автомат: %v\n%s", err, output)
	}
}
