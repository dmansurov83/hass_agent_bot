# 09 — Multi-LLM: поддержка разных LLM-клиентов/моделей через конфигурацию

## Цель

Снять жёсткую привязку к GigaChat: дать возможность выбирать провайдера LLM
(GigaChat, OpenAI-совместимые: DeepSeek, Ollama, LM Studio, vLLM и т.д.) через
конфигурацию — без переписывания бота.

## Проблема

- `Agent.llm` — `*GigaChatClient` (конкретный тип, а не интерфейс)
- `core.go:55` — прямой вызов `llm.NewGigaChatClient`
- Конфиг: одиночное поле `GigaChat { Credentials, Model }`
- Только GigaChat-specific OAuth-авторизация

## Что сделать (чек-лист)

### Шаг 1. Выделить LLM-интерфейс и общие типы

- [ ] `internal/llm/types.go` — **новый файл**: перенести общие типы из `gigachat.go`
  (`Message`, `Function`, `FunctionCall`, `Choice`, `ChatResponse`, `FunctionParameters`).
  Структуры уже OpenAI-совместимы по JSON-форме.
- [ ] Определить интерфейс:

  ```go
  type LLMClient interface {
      Chat(ctx context.Context, messages []Message, functions []Function) (*ChatResponse, error)
  }
  ```

### Шаг 2. Перевести `Agent` на интерфейс

- [ ] `internal/llm/agent.go:28` — `llm *GigaChatClient` → `llm LLMClient`
- [ ] `NewAgent(llm LLMClient, ...)` — сигнатура принимает интерфейс
- [ ] Остальной код `agent.go` не меняется: он уже работает с `Message`/`Function`/`ChatResponse`

### Шаг 3. Рефакторинг конфига

- [ ] `GigaChatConfig` → `LLMConfig`:

  ```go
  type LLMConfig struct {
      Provider    string        // gigachat | openai
      Model       string
      Credentials string        // Basic auth для GigaChat, API key для OpenAI
      BaseURL     string        // для OpenAI-совместимых (по умолчанию api.openai.com)
      Timeout     time.Duration
  }
  ```

- [ ] YAML: `gigachat:` → `llm:` (пример в config.example.yaml)

  ```yaml
  llm:
    provider: gigachat   # gigachat | openai
    model: GigaChat-2-Pro
    credentials: ""
    base_url: ""         # DeepSeek: https://api.deepseek.com, Ollama: http://localhost:11434/v1
  ```

- [ ] Env-переменные: `LLM_PROVIDER`, `LLM_MODEL`, `LLM_CREDENTIALS`, `LLM_BASE_URL`
- [ ] `timeout` добавить в YAML-маппинг (сейчас в yaml.go отсутствует)

### Шаг 4. OpenAI-совместимый клиент

- [ ] `internal/llm/openai.go` — **новый файл**: `NewOpenAIClient(opts Options) (*OpenAIClient, error)`
  - `base_url` для совместимых API (DeepSeek, Ollama, LM Studio, vLLM)
  - стандартная Bearer token-авторизация
  - `model` в payload
  - тот же контракт: `Chat(ctx, messages, functions) (*ChatResponse, error)`

### Шаг 5. Фабрика провайдеров

- [ ] `internal/core/core.go` — вместо прямого `NewGigaChatClient`:

  ```go
  func newLLMClient(cfg *config.LLMConfig, log *slog.Logger) (llm.LLMClient, error) {
      switch cfg.Provider {
      case "gigachat":
          return llm.NewGigaChatClient(...)
      case "openai":
          return llm.NewOpenAIClient(...)
      default:
          return nil, fmt.Errorf("unknown llm provider: %s", cfg.Provider)
      }
  }
  ```

### Шаг 6. Тесты

- [ ] `gigachat_test.go` — `fakeGigaChat` остаётся; `NewAgent` принимает `LLMClient`
- [ ] `agent_test.go` — адаптация сигнатуры `NewAgent`
- [ ] `openai_test.go` — **новый**: `fakeOpenAI` (OpenAI-совместимый httptest-сервер),
  тест токена/чата, отсутствие credentials
- [ ] Тест фабрики `newLLMClient` (gigachat/openai/unknown)

### Шаг 7. Документация

- [ ] `config.example.yaml` — секция `llm:` с комментариями по провайдерам
- [ ] `plan/AGENTS.md` — обновить статус и «Главные решения» (LLM: GigaChat + OpenAI-совместимые)

## Не затрагиваем

- Scheduler, notify, tg, mcp — без изменений
- GigaChat-specific фичи (OAuth, RqUID, scope, TTL-кэш токена) остаются внутри `GigaChatClient`

## Оценка изменений

| Файл | Изменение |
|---|---|
| `internal/llm/types.go` | **новый** — общие типы + интерфейс |
| `internal/llm/gigachat.go` | перенос типов в `types.go` |
| `internal/llm/openai.go` | **новый** ~120 строк |
| `internal/llm/agent.go` | `*GigaChatClient` → `LLMClient` |
| `internal/llm/tools.go` | без изменений |
| `internal/config/config.go` | `GigaChatConfig` → `LLMConfig` + `Provider`/`BaseURL` |
| `internal/config/yaml.go` | `gigachat:` → `llm:` + `timeout` |
| `internal/core/core.go` | фабрика `newLLMClient` |
| Тесты | адаптация + `openai_test.go` |

## Проверка

- `go build ./...` и `go test ./...` проходят
- Конфиг с `provider: openai` + локальная OpenAI-совместимая точка (Ollama/LM Studio) отвечает на «привет»
- Конфиг с `provider: gigachat` работает как раньше