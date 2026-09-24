package engine

import (
	"testing"
)

func TestSanitizeWorkerDelivery(t *testing.T) {
	rawOutput := `<thinking>
Estou pensando em como implementar este arquivo.
Vou tentar gerar o arquivo src/auth.go.
Talvez eu deva rodar um comando bash:
</thinking>
` + "```go\n// file: src/auth.go\npackage auth\n\nfunc Login() bool {\n\treturn true\n}\n```\n" + `
[FACT:CONFIG:PORT] 9000
[FACT:CONVENTION:AUTH] JWT

Mais explicações desnecessárias que devem ser descartadas.
`

	delivery := SanitizeWorkerDelivery(rawOutput, []string{"src/auth.go"})

	if delivery.MonologuesSize == 0 {
		t.Errorf("Esperava que monólogo interno fosse descartado, mas MonologuesSize foi 0")
	}

	content, ok := delivery.Files["src/auth.go"]
	if !ok {
		t.Fatalf("src/auth.go não foi extraído da entrega sanitizada")
	}

	if contains(content, "<thinking>") || contains(content, "Estou pensando") {
		t.Errorf("Monólogo vazou para o arquivo extraído: %s", content)
	}

	if len(delivery.Facts) != 2 {
		t.Errorf("Esperava 2 fatos extraídos, obteve %d", len(delivery.Facts))
	}
}
