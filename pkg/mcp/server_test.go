package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPServerTools(t *testing.T) {
	tempDir := t.TempDir()

	var stdin bytes.Buffer
	var stdout bytes.Buffer

	// 1. Initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	stdin.WriteString(initReq)

	// 2. List tools request
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	stdin.WriteString(listReq)

	// 3. Write memory tool call
	writeReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mcp__overclock__memory_write","arguments":{"category":"CONFIG","key":"DB_PORT","value":"5432"}}}` + "\n"
	stdin.WriteString(writeReq)

	// 4. Read memory tool call
	readReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"mcp__overclock__memory_read","arguments":{"key":"db_port"}}}` + "\n"
	stdin.WriteString(readReq)

	server := NewServer(tempDir, &stdin, &stdout)
	err := server.Serve()
	if err != nil {
		t.Fatalf("Serve falhou: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("Esperava 4 respostas JSON-RPC, obteve %d:\n%s", len(lines), stdout.String())
	}

	// Verify init
	var r1 JSONRPCResponse
	_ = json.Unmarshal([]byte(lines[0]), &r1)
	if r1.Error != nil {
		t.Errorf("Init retornou erro: %+v", r1.Error)
	}

	// Verify read response
	var r4 JSONRPCResponse
	_ = json.Unmarshal([]byte(lines[3]), &r4)
	resMap, _ := r4.Result.(map[string]interface{})
	content, _ := resMap["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("Leitura da memória não retornou conteúdo")
	}
	firstPart := content[0].(map[string]interface{})
	text := firstPart["text"].(string)
	if !strings.Contains(text, "5432") {
		t.Errorf("Esperava valor 5432 na leitura da memória, obteve: %s", text)
	}
}
