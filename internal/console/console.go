package console

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/llm"
	"hass-agent-bot/internal/notify"
	"hass-agent-bot/internal/scheduler"
)

const ownerChatID = 0

type Console struct {
	agent  *llm.Agent
	sched  *scheduler.Engine
	notify *notify.Engine
	ha     *hare.Client
	log    *slog.Logger

	in  *bufio.Scanner
	out *os.File
}

func New(agent *llm.Agent, sched *scheduler.Engine, nf *notify.Engine, haCli *hare.Client, log *slog.Logger) *Console {
	if log == nil {
		log = slog.Default()
	}
	return &Console{
		agent:  agent,
		sched:  sched,
		notify: nf,
		ha:     haCli,
		log:    log,
		in:     bufio.NewScanner(os.Stdin),
		out:    os.Stdout,
	}
}

func (c *Console) Start(ctx context.Context) error {
	c.log.Info("console: channel started")
	fmt.Fprintln(c.out, "Консольный канал активен. Введи /help для списка команд.")
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		fmt.Fprint(c.out, "> ")
		if !c.in.Scan() {
			return nil
		}

		line := strings.TrimSpace(c.in.Text())
		if line == "" {
			continue
		}

		if err := c.handleLine(ctx, line); err != nil {
			c.log.Error("console: command error", "error", err)
		}
	}
}

func (c *Console) Close(ctx context.Context) {
	c.log.Info("console: channel closed")
}

func (c *Console) SendMessage(ctx context.Context, chatID int64, text string) (int, error) {
	if chatID != 0 && chatID != ownerChatID {
		c.log.Debug("console: skipping message for foreign chat", "chat_id", chatID)
		return 0, nil
	}
	return c.print(text), nil
}

func (c *Console) SendNotification(ctx context.Context, text string) {
	c.print("Уведомление: " + text)
}

func (c *Console) print(text string) int {
	fmt.Fprintln(c.out, "Bot:", text)
	return 0
}

func (c *Console) handleLine(ctx context.Context, line string) error {
	text := line
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		cmd := parts[0]

		switch cmd {
		case "/start":
			c.print(startText())
			return nil
		case "/help":
			c.print(helpText())
			return nil
		case "/list":
			return c.list(ctx)
		case "/status":
			return c.status(ctx)
		case "/reset":
			if c.agent != nil {
				c.agent.Reset(ownerChatID)
			}
			c.print("История диалога сброшена.")
			return nil
		case "/timers":
			return c.timers(ctx)
		case "/quiet":
			return c.quiet(ctx, parts)
		}
	}

	if c.agent == nil {
		c.print("Ассистент не инициализирован. Попробуй /help.")
		return nil
	}

	c.print("Думаю...")
	ctx = llm.WithChatID(ctx, ownerChatID)
	reply, err := c.agent.HandleMessage(ctx, text)
	if err != nil {
		c.print(fmt.Sprintf("Ошибка: %v", err))
		return nil
	}
	c.print(reply)
	return nil
}

func (c *Console) list(ctx context.Context) error {
	if c.ha == nil {
		c.print("Home Assistant не подключён.")
		return nil
	}

	states, err := c.ha.States(ctx)
	if err != nil {
		c.print(fmt.Sprintf("Ошибка получения списка устройств: %v", err))
		return nil
	}
	if len(states) == 0 {
		c.print("Нет доступных устройств.")
		return nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Устройств в Home Assistant: %d\n", len(states)))
	for _, s := range states {
		fn := s.Attributes["friendly_name"]
		name := s.EntityID
		if f, ok := fn.(string); ok && f != "" {
			name = f
		}
		sb.WriteString(fmt.Sprintf("— %s (%s): %s\n", name, s.EntityID, s.State))
	}
	c.print(strings.TrimRight(sb.String(), "\n"))
	return nil
}

func (c *Console) status(ctx context.Context) error {
	if c.ha == nil {
		c.print("Home Assistant не подключён.")
		return nil
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := c.ha.States(ctxTimeout); err != nil {
		c.print(fmt.Sprintf("Home Assistant: недоступен (%v)", err))
		return nil
	}
	c.print("Подключение к Home Assistant: OK")
	return nil
}

func (c *Console) timers(ctx context.Context) error {
	if c.sched == nil {
		c.print("Планировщик не инициализирован.")
		return nil
	}

	jobs := c.sched.List()
	if len(jobs) == 0 {
		c.print("Активных таймеров нет.")
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Активные таймеры:\n")
	for _, j := range jobs {
		label := j.Label
		if label == "" {
			label = j.Action.Tool
		}
		when := "?"
		if j.Type == scheduler.TypeTimer {
			when = "в " + j.RunAt.Local().Format("02.01 15:04")
		} else {
			when = "расписание: " + j.CronExpr
		}
		fmt.Fprintf(&sb, "• %s\n   %s\n   ID: %s\n", label, when, j.ID)
	}
	c.print(strings.TrimRight(sb.String(), "\n"))
	return nil
}

func (c *Console) quiet(ctx context.Context, parts []string) error {
	if c.notify == nil {
		c.print("Уведомления не инициализированы.")
		return nil
	}

	hours := 1
	if len(parts) > 1 {
		if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
			hours = v
		}
	}

	c.notify.SetQuiet(time.Now().Add(time.Duration(hours) * time.Hour))
	c.print(fmt.Sprintf("Пауза уведомлений на %d ч.", hours))
	return nil
}

func startText() string {
	return "Привет! Я помощник для управления домом через Home Assistant.\n\nНапиши, что сделать, или используй /help для списка команд."
}

func helpText() string {
	return `Команды:
/start — приветствие
/help — эта справка
/list — список всех устройств в доме
/status — статус подключения к Home Assistant
/reset — сбросить историю диалога
/timers — список активных таймеров и расписаний
/quiet [часы] — пауза уведомлений на N часов (по умолчанию 1)

Просто напиши текстом, что хочешь сделать, например:
— включи свет в гостиной
— какая температура на улице
— выключи чайник через 15 минут
— запусти режим кино`
}
