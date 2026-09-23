# Makefile for overclock CLI

BINARY_NAME=overclock
BUILD_DIR=bin
INSTALL_PATH=/usr/local/bin
GO_FILES=$(shell find . -type f -name '*.go')

.PHONY: all build static test clean install uninstall

all: build

build:
	@echo "🔨 Compilando $(BINARY_NAME)..."
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/overclock

static:
	@echo "📦 Gerando binário estático otimizado (CGO_ENABLED=0)..."
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=1.0.0" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/overclock
	@echo "✅ Binário estático gerado em $(BUILD_DIR)/$(BINARY_NAME)"
	@ls -lh $(BUILD_DIR)/$(BINARY_NAME)

test:
	@echo "🧪 Executando testes unitários..."
	go test -v -race ./...

install: static
	@echo "🚀 Instalando em $(INSTALL_PATH)..."
	sudo install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "✅ Instalação concluída: $(INSTALL_PATH)/$(BINARY_NAME)"

uninstall:
	@echo "🗑️ Removendo de $(INSTALL_PATH)..."
	sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "✅ Removido."

clean:
	@echo "🧹 Limpando artefatos de build..."
	rm -rf $(BUILD_DIR)
