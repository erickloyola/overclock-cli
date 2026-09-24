# ⚡ OVERCLOCK-CLI v2.0.0

> **Motor de Alto Desempenho para Criação Autônoma de Projetos, Orquestração em Enxame (Multi-Agent Swarm) e Paralelismo Agressivo no Linux.**

Inspirado na arquitetura e filosofia de aceleração de hardware e orquestração distribuída, o **Overclock CLI v2.0.0** opera como uma fábrica autônoma de desenvolvimento de software em paralelo no Linux. Ele conecta-se nativamente ao **Antigravity CLI (`agy`)**, alavancando os modelos de raciocínio de ponta da sua assinatura Google (como `gemini-3.8-flash-high`) sem restrições de chaves de API, combinando **Memória Compartilhada (Blackboard)**, **Grafo de Dependências (DAG)**, **Inspeção de Sistema Local** e **Rotação Multi-Contas**.

---

## 🚀 O que há de Novo na Versão 2.0.0

### 1. 🧠 Criação Autônoma de Projetos (`overclock create`)
Em vez de fatiar texto arbitrariamente, o Overclock agora opera uma **linha de montagem de instâncias especializadas**:
1. **Instância 1 (Arquiteto & Planejador):** Transforma o prompt em um blueprint de engenharia completo, definindo tech stack poliglota, árvore de arquivos, dependências e contratos de interfaces antecipados.
2. **Instância 2 (Verificador de Distribuição & DAG):** Audita o plano, remove dependências circulares e garante que manifestos vitais (`go.mod`, `package.json`, `Cargo.toml`, `requirements.txt`) estejam presentes antes de gastar tempo de workers.
3. **Instâncias 3..N (Workers Concorrentes com `-j 8`):** Geram o código em paralelo respeitando a ordem do grafo (Fundação ➔ Serviços Centrais ➔ Telas/Componentes ➔ Entrypoints).
4. **Instância 4 (Supervisor de Coerência & QA):** Audita imports cruzados, detecta arquivos vazios ou incompletos e aplica patches cirúrgicos automáticos se houver divergências.
5. **Materializador no Disco:** Grava os arquivos diretamente na pasta alvo (`--out-dir`) e salva o snapshot do projeto em `.overclock/state.json`.

### 2. ⚡ Memória Compartilhada em RAM (Blackboard Pattern)
As instâncias do `agy` são isoladas, mas o processo Go do Overclock atua como o maestro central:
- **Tabela de Contratos Globais:** Interfaces TypeScript, structs Go e schemas compartilhados são injetados dinamicamente no prompt de cada worker.
- **Isolamento de Contexto:** Nenhum worker sofre de poluição de contexto; cada um recebe apenas seus contratos e as assinaturas dos arquivos de que depende.
- **Persistência de Estado:** Todo o estado do projeto é salvo em `.overclock/state.json` para auditoria ou retomada.

### 3. 🔍 Auto-Descoberta de Ambiente e Inspeção Local (`pkg/inspector`)
- O Overclock detecta automaticamente se você tem `bun`, `pnpm`, `npm`, `go`, `cargo`, `python3` ou `docker` instalados.
- Se você tem `bun`, ele prefere `bun` para velocidade extrema.
- Suporta injeção de schemas e código legado via `--context ./schema.sql` ou `--from ./meu-projeto`.

### 4. 🛠️ Lifecycle Hooks do Sistema Operacional
- `-i, --install`: Roda automaticamente o gerenciador de pacotes (`bun install`, `npm install`, `go mod tidy`, `pip install`, etc.) dentro da pasta gerada.
- `--verify`: Executa o compilador real do sistema (`go build`, `npm run build`, `cargo check`). Se o compilador reportar um erro, o Overclock captura a saída do terminal e aciona o **Supervisor de Auto-Correção** para ajustar o arquivo com defeito!
- `--git`: Inicializa o repositório Git e realiza o primeiro commit automaticamente.

### 5. 🎼 Orquestração Multi-Agente Avançada (Maestro Protocol)
- **Event-Driven Handoff (Zero Polling):** Sem laços de espera ativa gastando CPU ou tokens (`sleep 100ms`). O Maestro desperta instantaneamente e de forma reativa no recebimento de canais de eventos dos workers.
- **Validation Gates (Portões de Validação Estritos):**
  - **Gate 1 (Blueprint & Contratos):** Garante especificação e grafo acíclico antes de instanciar workers.
  - **Gate 2 (Estágio & Validação Unitária):** Audita integridade e sintaxe de cada lote gerado. O próximo estágio só é instanciado se o Gate correspondente estiver verde (GREEN).
  - **Gate 3 (Integração & Consistência Global):** Valida imports cruzados e elimina divergências antes da entrega final.
- **Topologia Hub-and-Spoke & Clean Context Injection:** Filtro redacional central que descarta monólogos internos (`<thinking>`) e comandos de terminal falhos, injetando apenas contratos e interfaces limpas nos workers downstream.
- **Ciclo de Vida Efêmero (Auto-Teardown):** Workers encerram imediatamente após o handoff, liberando instâncias, semáforos e buffers.
- **Isolamento de Código via Git Worktrees (`--worktrees`):** Workers paralelos operam em branches e worktrees físicas isoladas (`.worktrees/task-*`), eliminando colisões de arquivo e *race conditions* no disco antes do merge final pelo Maestro.

---

## 📁 Estrutura do Projeto

```
overclock-cli/
├── cmd/
│   └── overclock/
│       └── main.go              # CLI entrypoint, subcomandos (create, accounts, switch, login)
├── pkg/
│   ├── memory/                  # 🧠 Memória Compartilhada (Blackboard, Contratos, DAG)
│   │   ├── blackboard.go        # Estado thread-safe (RWMutex), registro de arquivos e contratos
│   │   └── blackboard_test.go   # Testes de concorrência (-race) e resolução de DAG
│   ├── inspector/               # 🔍 Inspeção de ferramentas do SO e arquivos locais
│   │   └── inspector.go         # Auto-descoberta de toolchains (bun, go, python, node, etc.)
│   ├── planner/                 # 📐 Agente Arquiteto de Software
│   │   └── architect.go         # Blueprint JSON estrito com DAG e contratos globais
│   ├── verifier/                # 🔍 Agente Verificador de Distribuição
│   │   ├── auditor.go           # Detecção de dependências e auto-patching de manifestos
│   │   └── auditor_test.go      # Testes de validação de blueprints
│   ├── supervisor/              # 🕵️ Agente Supervisor de Consistência & QA
│   │   ├── reviewer.go          # Validador de imports cruzados e patches cirúrgicos
│   │   └── reviewer_test.go     # Testes de consistência
│   ├── engine/                  # ⚙️ Motores de Execução e Ciclo de Vida
│   │   ├── dag_runner.go        # Executor de tarefas concorrentes governado por canais e DAG
│   │   ├── materializer.go      # Gravação no disco, auto-instalação e compilação verificada
│   │   ├── agy_runner.go        # Subprocess runner para agy CLI com streaming de tokens
│   │   ├── orchestrator.go      # Coordenador de modos (Create, Pipe, Map, Lines)
│   │   ├── runner.go            # Interface abstrata de execução (Agy / API)
│   │   └── workerpool.go        # Pool genérico para processamento em lote
│   ├── auth/                    # 🔑 Autenticação e rotação de contas Google
│   ├── client/                  # 🌐 Cliente HTTP/2 e SSE para Gemini REST API
│   ├── pool/                    # 🔄 Rotação de chaves de API e cooldown
│   ├── pruner/                  # ✂️ Compressão e poda inteligente de tokens
│   └── ui/                      # 🎨 Formatador TTY, telemetria e cores ANSI
├── Makefile                     # Compilação estática e instalação
├── go.mod                       # Módulo Go puro (zero dependências externas)
└── README.md
```

---

## 🛠️ Instalação e Compilação

```bash
cd /home/erickloyola/overclock-cli
make build
install -m 755 bin/overclock ~/.local/bin/overclock
```

---

## 💻 Exemplos de Uso

### 1. Criação Acelerada de Projeto com 8 Workers (-j 8)
```bash
overclock create -j 8 --out-dir ./dashboard-app -i --verify \
  "Crie um dashboard em React + Vite + Tailwind para métricas de GPU com gráficos e temas dark/light"
```

### 2. Criar API em Go com Base em um Schema SQL Local
```bash
overclock create -j 8 --out-dir ./auth-api -i --verify --context ./schema.sql \
  "Crie uma API REST em Go com Gin, GORM e autenticação JWT para este schema"
```

### 3. Criar Projeto Full-Stack com Git Inicializado
```bash
overclock create -j 8 --out-dir ./chamados-app --git -i \
  "Crie um sistema de chamados com backend em FastAPI e frontend em Vite React"
```

### 4. Gerenciar Múltiplas Contas Google
```bash
# Listar contas e ver qual está ativa
overclock accounts

# Alternar para outra conta salva
overclock switch conta2

# Conectar nova conta Google via OAuth 2.0
overclock login --name trabalho
```

### 5. Processamento em Lote (Modo Map)
```bash
find ./src -name "*.go" | overclock map -j 8 "Identifique memory leaks ou problemas de concorrência"
```

### 6. Streaming Unix Pipeline
```bash
cat /var/log/nginx/error.log | overclock --prune "Resuma os erros críticos"
```
