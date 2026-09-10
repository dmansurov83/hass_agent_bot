# 11 — История диалога: per-chat + персистентность в JSON

## Проблемы

1. **История общая на все чаты** — `Agent.history` один, сообщения от разных пользователей смешиваются.
2. **Фоновые AI-задачи засоряют историю** — результат `agent.HandleMessage(prompt)` при срабатывании `schedule_ai_action` пишется в ту же `a.history`.
3. **История живёт только в памяти** — после перезапуска бота теряется весь контекст диалогов.

## Решение

### 0. Абстракция хранилища — интерфейс HistoryStore (заложено под будущее)

Агент не знает, где лежат данные (файл/БД) — только интерфейс. Сейчас — реализация `FileStore` на JSON-файлах, в будущем — `SQLiteStore`, `PostgresStore` и т.д. без изменения кода агента.

**`internal/llm/history.go` — новый файл:**
```go
// HistoryStore — persistence layer for per-chat dialog history.
type HistoryStore interface {
    Load(ctx context.Context) (map[int64][]Message, error)   // все чаты
    Save(ctx context.Context, chatID int64, msgs []Message) error
    Delete(ctx context.Context, chatID int64) error
    Close() error
}
```

**`internal/llm/history_file.go` — реализация на JSON-файлах:**
```go
type FileStore struct {
    dir string
    log *slog.Logger
}
func NewFileStore(dir string, log *slog.Logger) (*FileStore, error)  // MkdirAll
```
- Файлы: `<dir>/chat_<chatID>.json`
- `chatID` → `strconv.FormatInt` (безопасно, без интерполяции путей)
- Битый файл → лог + пропуск (не роняем бота)

**Фабрика (core.go):**
```go
func newHistoryStore(cfg *config.Config, log *slog.Logger) (HistoryStore, error) {
    return llm.NewFileStore(filepath.Join(cfg.DataDir, "history"), log)
    // в будущем: switch cfg.Storage.Type { case "sqlite": ... }
}
```

### 1. Per-chat история в памяти

`Agent.history []Message` → `histories map[int64][]Message` (ключ — chatID из context). Агент держит **кэш в памяти** + пишет через `HistoryStore`.

```go
type Agent struct {
    llm   LLMClient
    mcp   *hamcp.Client
    sched Scheduler
    log   *slog.Logger

    mu        sync.Mutex
    histories map[int64][]Message  // chatID → messages (кэш)
    store     HistoryStore         // персистентность (nil = не сохранять)
}
```

**NewAgent:**
```go
func NewAgent(llm LLMClient, mcpCli *hamcp.Client, sched Scheduler, store HistoryStore) *Agent
// при создании: histories = store.Load(ctx) (или пустая map при ошибке)
```

Загрузка — при старте один раз: `Load()` поднимает все чаты в память. Дальше только Save/Delete.

### 3. HandleMessage — per-chat + taskMode не сохраняет

```go
chatID := ChatIDFrom(ctx)
taskMode := taskModeFrom(ctx)

// ... ReAct loop ...
if !taskMode {
    history := a.histories[chatID]
    history = append(history,
        Message{Role: "user", Content: userText},
        Message{Role: "assistant", Content: final},
    )
    if len(history) > 10 {
        history = history[len(history)-10:]
    }
    a.histories[chatID] = history
    a.saveHistory(chatID)
}
```

### 4. Reset — по чату

```go
func (a *Agent) Reset(chatID int64) { /* удалить из map + удалить файл */ }
func (a *Agent) ResetAll()          { /* очистить всё + удалить файлы */ }
```

### 5. TG — адаптировать вызовы

- `tg.go` resetHandler: `b.agent.Reset()` → `b.agent.Reset(chatID)`
- `core.go` agentExecutor (фоновая задача): уже передаёт `WithTaskMode` → в историю не пишется.

### 6. Конфиг

В `config.yaml` уже есть `DataDir` (по умолчанию — рядом с бинарником). Ничего нового добавлять не надо — просто используем `filepath.Join(cfg.DataDir, "history")`. При старте создаём каталог (`os.MkdirAll`).

Возможность выбора хранилища без ломки интерфейса: добавить `storage.type: file|sqlite` в конфиг (опционально, можно позже — фабрика `newHistoryStore` уже готова к switch'у).

### Изменяемые файлы

| Файл | Что меняем |
|---|---|
| `internal/llm/history.go` | **НОВЫЙ** — интерфейс `HistoryStore` |
| `internal/llm/history_file.go` | **НОВЫЙ** — реализация `FileStore` (JSON-файлы) |
| `internal/llm/agent.go` | `history → histories map[int64][]Message`; `store`; HandleMessage: per-chat + taskMode не сохраняет; Reset(chatID), ResetAll |
| `internal/llm/types.go` | Не меняется (Message уже имеет json-теги) |
| `internal/llm/agent_test.go` | Тесты: новая сигнатура NewAgent + Reset; тест персистентности (tempdir) |
| `internal/tg/tg.go` | `resetHandler`: `Reset(chatID)` |
| `internal/core/core.go` | `newHistoryStore(cfg, log)`, `NewAgent(..., store)` |

### Проверка

1. **Изоляция чатов:** чат 1 «включи свет» → чат 2 «привет» → история не смешивается.
2. **Фоновые задачи:** AI-задача «через минуту расскажи анекдот» → после срабатывания /reset не показывает анекдот как часть диалога, файл истории не пополняется.
3. **Персистентность:** написать в чат → перезапустить бота → спросить «что я просил сделать?» → бот помнит контекст.
4. **Сброс:** /reset → файл чата удаляется, история пустая.
5. **Битый файл:** подложить мусор в chat_*.json → бот стартует, история чата пустая, ошибка в логе.