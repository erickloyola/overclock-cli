package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"overclock/pkg/mcp"
	"overclock/pkg/memory"
	"overclock/pkg/ui"
)

func handleMCP(args []string) {
	dir := "."
	for i := 0; i < len(args); i++ {
		if (args[i] == "--dir" || args[i] == "-d") && i+1 < len(args) {
			dir = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			dir = args[i]
		}
	}
	server := mcp.NewServer(dir, os.Stdin, os.Stdout)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server encerrou com erro: %v\n", err)
		os.Exit(1)
	}
}

func handleMemory(args []string) {
	if len(args) == 0 {
		showMemorySummary(".")
		return
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "facts":
		dir := "."
		if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
			dir = subArgs[0]
		}
		showMemoryFacts(dir)

	case "lessons":
		dir := "."
		if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
			dir = subArgs[0]
		}
		showMemoryLessons(dir)

	case "events":
		dir := "."
		if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
			dir = subArgs[0]
		}
		showMemoryEvents(dir)

	case "set":
		handleMemorySet(subArgs)

	case "history":
		if len(subArgs) == 0 {
			fmt.Println("Uso: overclock memory history <caminho/do/arquivo> [pasta_projeto]")
			return
		}
		filePath := subArgs[0]
		dir := "."
		if len(subArgs) > 1 {
			dir = subArgs[1]
		}
		showMemoryHistory(dir, filePath)

	case "rollback":
		if len(subArgs) < 2 {
			fmt.Println("Uso: overclock memory rollback <caminho/do/arquivo> <versao> [pasta_projeto]")
			return
		}
		filePath := subArgs[0]
		verStr := subArgs[1]
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			fmt.Printf("Versão inválida: %s\n", verStr)
			return
		}
		dir := "."
		if len(subArgs) > 2 {
			dir = subArgs[2]
		}
		executeRollback(dir, filePath, ver)

	case "global":
		handleMemoryGlobal(subArgs)

	case "-h", "--help", "help":
		printMemoryHelp()

	default:
		// Se o primeiro argumento for um diretório existente, exibe o resumo dele
		if stat, err := os.Stat(sub); err == nil && stat.IsDir() {
			showMemorySummary(sub)
		} else {
			printMemoryHelp()
		}
	}
}

func printMemoryHelp() {
	fmt.Println(`⚡ OVERCLOCK MEMORY - Gerenciamento e Inspeção de Memória Compartilhada

USO:
  overclock memory [PASTA]                             Exibe o resumo geral da memória do projeto
  overclock memory facts [PASTA]                       Lista todos os fatos operacionais gravados
  overclock memory lessons [PASTA]                     Lista lições aprendidas (erros e auto-correções)
  overclock memory events [PASTA]                      Exibe o log de eventos transacionais (events.jsonl)
  overclock memory set <KEY> <VALUE> [FLAGS] [PASTA]   Grava manualmente um fato na memória compartilhada
  overclock memory history <ARQUIVO> [PASTA]           Exibe o histórico de versões e revisões de um arquivo
  overclock memory rollback <ARQUIVO> <VERSAO> [PASTA] Reverte um arquivo para uma versão anterior salva
  overclock memory global [list|set|del]               Gerencia a memória global compartilhada entre projetos
  overclock mcp [--dir PASTA]                          Inicia o servidor MCP nativo (Model Context Protocol)

FLAGS PARA 'set':
  --category, -c   Categoria do fato (CONFIG, CONVENTION, DEP, RUNTIME, GENERAL) [padrão: GENERAL]
  --global, -g     Grava também na memória global do usuário (~/.config/overclock)`)
}

func showMemorySummary(dir string) {
	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil {
		fmt.Printf("❌ Nenhum estado do Overclock encontrado em '%s': %v\n", stateFile, err)
		return
	}

	manifest := bb.GetManifest()
	files := bb.GetAllFiles()
	facts := bb.GetFacts()
	lessons := bb.GetLessons()
	events, _ := memory.LoadEvents(dir)

	fmt.Println("\n" + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset)
	fmt.Println(" " + ui.Bold + ui.Green + "🧠 RESUMO DA MEMÓRIA COMPARTILHADA (OVERCLOCK BLACKBOARD)" + ui.Reset)
	fmt.Println(" " + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset)

	fmt.Printf("\n • " + ui.Bold + "Projeto:" + ui.Reset + "           %s\n", manifest.Name)
	fmt.Printf(" • " + ui.Bold + "Stack:" + ui.Reset + "             %s (%s)\n", manifest.Stack, manifest.PackageManager)
	fmt.Printf(" • " + ui.Bold + "Arquivos:" + ui.Reset + "          %d registrados\n", len(files))
	fmt.Printf(" • " + ui.Bold + "Fatos (OverMemory):" + ui.Reset + "%d registrados\n", len(facts))
	fmt.Printf(" • " + ui.Bold + "Lições Aprendidas:" + ui.Reset + " %d registradas\n", len(lessons))
	fmt.Printf(" • " + ui.Bold + "Eventos no Log:" + ui.Reset + "    %d transações gravadas\n", len(events))

	if len(facts) > 0 {
		fmt.Println("\n " + ui.Bold + "📋 Fatos Operacionais Recentes:" + ui.Reset)
		for i, f := range facts {
			if i >= 5 {
				fmt.Printf("   ... e mais %d fatos (use 'overclock memory facts')\n", len(facts)-5)
				break
			}
			fmt.Printf("   • [%s] %s: %s (%s)\n", f.Category, f.Key, f.Value, f.Source)
		}
	}

	if len(lessons) > 0 {
		fmt.Println("\n " + ui.Bold + "💡 Lições Aprendidas Recentes:" + ui.Reset)
		for i, l := range lessons {
			if i >= 3 {
				break
			}
			fmt.Printf("   • [%s] %s ➔ %s\n", l.Trigger, l.Pattern, l.Guidance)
		}
	}
	fmt.Println()
}

func showMemoryFacts(dir string) {
	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil {
		fmt.Printf("❌ Falha ao carregar estado de '%s': %v\n", dir, err)
		return
	}

	facts := bb.GetFacts()
	if len(facts) == 0 {
		fmt.Println("ℹ️ Nenhum fato registrado na memória compartilhada deste projeto.")
		return
	}

	fmt.Printf("\n" + ui.Bold + "📋 FATOS OPERACIONAIS (OVERMEMORY) - %s (%d fatos)" + ui.Reset + "\n\n", dir, len(facts))
	for _, f := range facts {
		fmt.Printf(" • " + ui.Cyan + "[%s]" + ui.Reset + " " + ui.Bold + "%s" + ui.Reset + "\n", f.Category, f.Key)
		fmt.Printf("   Valor: %s\n", f.Value)
		fmt.Printf("   Fonte: %s | Registrado: %s\n\n", f.Source, f.CreatedAt.Format("02/01/2006 15:04:05"))
	}
}

func showMemoryLessons(dir string) {
	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil {
		fmt.Printf("❌ Falha ao carregar estado de '%s': %v\n", dir, err)
		return
	}

	lessons := bb.GetLessons()
	if len(lessons) == 0 {
		fmt.Println("ℹ️ Nenhuma lição aprendida registrada para este projeto.")
		return
	}

	fmt.Printf("\n" + ui.Bold + "💡 LIÇÕES APRENDIDAS (FEEDBACK LOOP) - %s (%d lições)" + ui.Reset + "\n\n", dir, len(lessons))
	for _, l := range lessons {
		fileStr := ""
		if l.File != "" {
			fileStr = fmt.Sprintf(" (%s)", l.File)
		}
		fmt.Printf(" • " + ui.Yellow + "[%s%s]" + ui.Reset + "\n", l.Trigger, fileStr)
		fmt.Printf("   Problema: %s\n", l.Pattern)
		fmt.Printf("   Orientação: %s\n\n", l.Guidance)
	}
}

func showMemoryEvents(dir string) {
	events, err := memory.LoadEvents(dir)
	if err != nil {
		fmt.Printf("❌ Falha ao ler events.jsonl em '%s': %v\n", dir, err)
		return
	}
	if len(events) == 0 {
		fmt.Println("ℹ️ Nenhum evento no log transacional.")
		return
	}

	fmt.Printf("\n" + ui.Bold + "📜 LOG TRANSACIONAL DE EVENTOS (%d registros):" + ui.Reset + "\n\n", len(events))
	for _, ev := range events {
		fmt.Printf(" • [%s] " + ui.Green + "%-16s" + ui.Reset + " %+v\n",
			ev.Timestamp.Format("15:04:05.000"), ev.Type, ev.Payload)
	}
	fmt.Println()
}

func handleMemorySet(args []string) {
	if len(args) < 2 {
		fmt.Println("Uso: overclock memory set <KEY> <VALUE> [--category CAT] [--global] [pasta]")
		return
	}

	key := args[0]
	val := args[1]
	category := memory.FactCategoryGeneral
	isGlobal := false
	dir := "."

	for i := 2; i < len(args); i++ {
		a := args[i]
		if (a == "--category" || a == "-c") && i+1 < len(args) {
			category = memory.FactCategory(strings.ToUpper(args[i+1]))
			i++
		} else if a == "--global" || a == "-g" {
			isGlobal = true
		} else if !strings.HasPrefix(a, "-") {
			dir = a
		}
	}

	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err == nil && bb != nil {
		fact := bb.RecordFact(category, key, val, "User-CLI")
		_ = memory.AppendEvent(dir, memory.EventFactRecorded, fact)
		_ = bb.SaveState(dir)
		fmt.Printf("✅ Fato [%s] '%s' registrado no projeto em '%s'.\n", category, key, dir)
	}

	if isGlobal {
		gm, gmErr := memory.LoadGlobalMemory()
		if gmErr == nil {
			gm.SetFact(category, key, val, "User-CLI")
			_ = gm.Save()
			fmt.Printf("🌐 Fato [%s] '%s' registrado também na Memória Global.\n", category, key)
		}
	}
}

func showMemoryHistory(dir, filePath string) {
	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil {
		fmt.Printf("❌ Falha ao carregar estado de '%s': %v\n", dir, err)
		return
	}

	f, ok := bb.GetFile(filePath)
	if !ok {
		fmt.Printf("❌ Arquivo '%s' não encontrado na memória do projeto.\n", filePath)
		return
	}

	fmt.Printf("\n" + ui.Bold + "📜 HISTÓRICO DE REVISÕES: %s" + ui.Reset + "\n", filePath)
	fmt.Printf("Versão Atual: v%d (%d bytes, hash: %.8s) por %s\n\n", f.Version, f.Bytes, f.Hash, f.GeneratedBy)

	revs := bb.GetFileRevisions(filePath)
	if len(revs) == 0 {
		fmt.Println("ℹ️ Nenhuma revisão anterior arquivada (arquivo em versão inicial).")
		return
	}

	for _, rev := range revs {
		fmt.Printf(" • " + ui.Cyan + "Versão %d" + ui.Reset + " (%d bytes, hash: %.8s) - por %s [%s]\n",
			rev.Version, rev.Bytes, rev.Hash, rev.ModifiedBy, rev.Timestamp.Format("02/01 15:04:05"))
		if rev.Reason != "" {
			fmt.Printf("   Motivo: %s\n", rev.Reason)
		}
	}
	fmt.Println()
}

func executeRollback(dir, filePath string, version int) {
	stateFile := filepath.Join(dir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil {
		fmt.Printf("❌ Falha ao carregar estado de '%s': %v\n", dir, err)
		return
	}

	err = bb.RollbackFile(filePath, version)
	if err != nil {
		fmt.Printf("❌ Falha no rollback: %v\n", err)
		return
	}

	// Persist state & materializer
	_ = bb.SaveState(dir)
	_ = memory.AppendEvent(dir, memory.EventFileRollback, map[string]interface{}{
		"path":    filePath,
		"version": version,
	})

	if f, ok := bb.GetFile(filePath); ok {
		fullPath := filepath.Join(dir, filePath)
		_ = os.WriteFile(fullPath, []byte(f.Content), 0644)
	}

	fmt.Printf("✅ Sucesso: Arquivo '%s' revertido para a versão %d e gravado no disco!\n", filePath, version)
}

func handleMemoryGlobal(args []string) {
	gm, err := memory.LoadGlobalMemory()
	if err != nil {
		fmt.Printf("❌ Falha ao carregar memória global: %v\n", err)
		return
	}

	if len(args) == 0 || args[0] == "list" {
		facts := gm.GetFacts()
		fmt.Println("\n" + ui.Bold + "🌐 MEMÓRIA GLOBAL DO OVERCLOCK" + ui.Reset + " (" + memory.GetGlobalMemoryPath() + ")")
		fmt.Printf("Total de Fatos: %d | Convenções: %d\n\n", len(facts), len(gm.Conventions))

		for _, f := range facts {
			fmt.Printf(" • [%s] %s = %s\n", f.Category, f.Key, f.Value)
		}
		if len(gm.Conventions) > 0 {
			fmt.Println("\nConvenções:")
			for _, c := range gm.Conventions {
				fmt.Printf(" • %s\n", c)
			}
		}
		fmt.Println()
		return
	}

	cmd := args[0]
	switch cmd {
	case "set":
		if len(args) < 3 {
			fmt.Println("Uso: overclock memory global set <KEY> <VALUE> [CATEGORIA]")
			return
		}
		cat := memory.FactCategoryGeneral
		if len(args) > 3 {
			cat = memory.FactCategory(strings.ToUpper(args[3]))
		}
		gm.SetFact(cat, args[1], args[2], "User-CLI")
		_ = gm.Save()
		fmt.Printf("✅ Fato global [%s] '%s' registrado.\n", cat, args[1])

	case "del", "delete":
		if len(args) < 2 {
			fmt.Println("Uso: overclock memory global del <KEY>")
			return
		}
		if gm.DeleteFact(args[1]) {
			_ = gm.Save()
			fmt.Printf("✅ Fato global '%s' removido.\n", args[1])
		} else {
			fmt.Printf("Fato global '%s' não encontrado.\n", args[1])
		}

	case "convention":
		if len(args) < 2 {
			fmt.Println("Uso: overclock memory global convention \"texto da convenção\"")
			return
		}
		gm.AddConvention(args[1])
		_ = gm.Save()
		fmt.Printf("✅ Convenção global adicionada: %s\n", args[1])

	default:
		fmt.Println("Comando desconhecido. Use: overclock memory global [list|set|del|convention]")
	}
}
