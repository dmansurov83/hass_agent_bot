package tg

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	hare "hass-agent-bot/internal/ha/rest"
	"hass-agent-bot/internal/llm"
	"hass-agent-bot/internal/notify"
	"hass-agent-bot/internal/scheduler"
)

type Bot struct {
	cli    *bot.Bot
	ha     *hare.Client
	agent  *llm.Agent
	sched  *scheduler.Engine
	notify *notify.Engine
	allow  map[int64]bool
	first  int64 // first allowed user ID (for notify)
	log    *slog.Logger
}

type Option func(*Bot)

func WithAgent(agent *llm.Agent) Option {
	return func(b *Bot) { b.agent = agent }
}

func WithScheduler(sched *scheduler.Engine) Option {
	return func(b *Bot) { b.sched = sched }
}

func WithNotify(ne *notify.Engine) Option {
	return func(b *Bot) { b.notify = ne }
}

func New(token string, allowIDs []int64, haCli *hare.Client, opts ...Option) (*Bot, error) {
	b := &Bot{
		ha:    haCli,
		allow: make(map[int64]bool, len(allowIDs)),
		log:   slog.Default(),
	}
	for _, id := range allowIDs {
		b.allow[id] = true
	}
	if len(allowIDs) > 0 {
		b.first = allowIDs[0]
	}
	for _, o := range opts {
		o(b)
	}

	botOpts := []bot.Option{
		bot.WithMiddlewares(b.allowlistMiddleware),
		bot.WithMessageTextHandler("/start", bot.MatchTypeExact, b.startHandler),
		bot.WithMessageTextHandler("/help", bot.MatchTypeExact, b.helpHandler),
		bot.WithMessageTextHandler("/list", bot.MatchTypeExact, b.listHandler),
		bot.WithMessageTextHandler("/status", bot.MatchTypeExact, b.statusHandler),
		bot.WithMessageTextHandler("/reset", bot.MatchTypeExact, b.resetHandler),
		bot.WithMessageTextHandler("/timers", bot.MatchTypeExact, b.timersHandler),
		bot.WithMessageTextHandler("/quiet", bot.MatchTypeExact, b.quietHandler),
		bot.WithDefaultHandler(b.textHandler),
	}

	cli, err := bot.New(token, botOpts...)
	if err != nil {
		return nil, fmt.Errorf("tg: create bot: %w", err)
	}
	b.cli = cli

	return b, nil
}

func (b *Bot) Start(ctx context.Context) error {
	b.cli.Start(ctx)
	return nil
}

func (b *Bot) Close(ctx context.Context) {
	b.cli.Close(ctx)
}

func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string) (int, error) {
	msg, err := b.cli.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		b.log.Error("tg: send message", "chat_id", chatID, "error", err)
		return 0, err
	}
	return msg.ID, nil
}

func (b *Bot) allowedUser(update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return false
	}
	return b.allow[update.Message.From.ID]
}

func (b *Bot) allowlistMiddleware(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
		if !b.allowedUser(update) {
			if update.Message != nil && update.Message.From != nil {
				b.log.Warn("tg: blocked unauthorized user",
					"user_id", update.Message.From.ID,
					"username", update.Message.From.Username)
			}
			return
		}
		next(ctx, tgBot, update)
	}
}

func (b *Bot) startHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Привет! Я помощник для управления домом через Home Assistant.\n\nНапиши, что сделать, или используй /help для списка команд.",
	})
}

func (b *Bot) helpHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text: `Команды:
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
— запусти режим кино`,
	})
}

func (b *Bot) listHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	if b.ha == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Home Assistant не подключён."})
		return
	}

	states, err := b.ha.States(ctx)
	if err != nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Ошибка получения списка устройств: %v", err)})
		return
	}

	if len(states) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Нет доступных устройств."})
		return
	}

	// Перечисляем все устройства с human-friendly названиями
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Устройств в Home Assistant: %d\n\n", len(states)))
	for _, s := range states {
		fn := s.Attributes["friendly_name"]
		name := s.EntityID
		if f, ok := fn.(string); ok && f != "" {
			name = f
		}
		sb.WriteString(fmt.Sprintf("— %s (%s): %s\n", name, s.EntityID, s.State))
	}

	out := sb.String()
	// truncate if huge
	const maxLen = 3800
	if len(out) > maxLen {
		out = out[:maxLen] + "\n…"
	}
	tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: out})
}

func (b *Bot) statusHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	if b.ha == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Home Assistant не подключён."})
		return
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := b.ha.States(ctxTimeout); err != nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Home Assistant: недоступен (%v)", err)})
		return
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Подключение к Home Assistant: OK",
	})
}

func (b *Bot) textHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	if b.agent == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "Ассистент не инициализирован. Попробуй /help.",
		})
		return
	}

	// Показываем индикатор, затем заменим его на ответ
	msg, err := tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "⏳ Думаю...",
	})
	if err != nil {
		b.log.Error("tg: send thinking", "error", err)
		return
	}

	// Send to LLM agent (carry the chat ID so AI tasks know where to reply)
	ctx = llm.WithChatID(ctx, chatID)
	reply, err := b.agent.HandleMessage(ctx, text)
	if err != nil {
		b.log.Error("tg: agent error", "error", err)
		if _, editErr := tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: msg.ID,
			Text:      fmt.Sprintf("Ошибка: %v", err),
		}); editErr != nil {
			b.log.Error("tg: edit error message", "error", editErr)
		}
		return
	}

	// Заменяем "⏳ Думаю..." на итоговый ответ
	if _, editErr := tgBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msg.ID,
		Text:      reply,
	}); editErr != nil {
		// Если редактирование не удалось (например, слишком длинный текст) — шлём новым сообщением
		b.log.Error("tg: edit reply", "error", editErr)
		b.SendMessage(ctx, chatID, reply)
	}
}

func (b *Bot) resetHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	if b.agent != nil {
		b.agent.Reset(chatID)
	}
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "История диалога сброшена.",
	})
}

func (b *Bot) timersHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	if b.sched == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Планировщик не инициализирован."})
		return
	}

	jobs := b.sched.List()
	if len(jobs) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Активных таймеров нет."})
		return
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
fmt.Fprintf(&sb, "🕐 %s\n   %s\n   ID: %s\n", label, when, j.ID)
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: sb.String()})
}

func (b *Bot) quietHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	if b.notify == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Уведомления не инициализированы."})
		return
	}

	hours := 1
	fields := strings.Fields(update.Message.Text)
	if len(fields) > 1 {
		if v, err := strconv.Atoi(fields[1]); err == nil && v > 0 {
			hours = v
		}
	}

	b.notify.SetQuiet(time.Now().Add(time.Duration(hours) * time.Hour))
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Пауза уведомлений на %d ч.", hours),
	})
}

// SendNotification sends a message from the notify engine to the owner's chat.
func (b *Bot) SendNotification(ctx context.Context, text string) {
	if b.first == 0 {
		b.log.Warn("tg: no owner chat for notification")
		return
	}
	b.SendMessage(ctx, b.first, text)
}