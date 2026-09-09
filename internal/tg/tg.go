package tg

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	hamcp "hass-agent-bot/internal/ha/mcp"
)

type Bot struct {
	cli   *bot.Bot
	mcp   *hamcp.Client
	allow map[int64]bool
	log   *slog.Logger
}

func New(token string, allowIDs []int64, mcpCli *hamcp.Client, opts ...bot.Option) (*Bot, error) {
	b := &Bot{
		mcp:   mcpCli,
		allow: make(map[int64]bool, len(allowIDs)),
		log:   slog.Default(),
	}
	for _, id := range allowIDs {
		b.allow[id] = true
	}

	opts = append(opts,
		bot.WithMiddlewares(b.allowlistMiddleware),
		bot.WithMessageTextHandler("/start", bot.MatchTypeExact, b.startHandler),
		bot.WithMessageTextHandler("/help", bot.MatchTypeExact, b.helpHandler),
		bot.WithMessageTextHandler("/list", bot.MatchTypeExact, b.listHandler),
		bot.WithMessageTextHandler("/status", bot.MatchTypeExact, b.statusHandler),
		bot.WithDefaultHandler(b.textHandler),
	)

	cli, err := bot.New(token, opts...)
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

func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string) {
	if _, err := b.cli.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}); err != nil {
		b.log.Error("tg: send message", "chat_id", chatID, "error", err)
	}
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

Просто напиши текстом, что хочешь сделать, например:
— включи свет в гостиной
— какая температура на улице
— выключи чайник через 15 минут
— запусти режим кино`,
	})
}

func (b *Bot) listHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	if b.mcp == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Home Assistant не подключён."})
		return
	}

	entities, err := b.mcp.ListEntities(ctx)
	if err != nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Ошибка получения списка устройств: %v", err)})
		return
	}

	if len(entities) == 0 {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Нет доступных устройств (проверь Exposed entities в HA)."})
		return
	}

	var sb strings.Builder
	sb.WriteString("Устройства:\n")
	for _, e := range entities {
		name := e.FriendlyName
		if name == "" {
			name = e.EntityID
		}
		fmt.Fprintf(&sb, "• %s — %s (%s)\n", name, e.State, e.EntityID)
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: sb.String()})
}

func (b *Bot) statusHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID

	if b.mcp == nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Home Assistant не подключён."})
		return
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	entities, err := b.mcp.ListEntities(ctxTimeout)
	if err != nil {
		tgBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Home Assistant: недоступен (%v)", err)})
		return
	}

	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Подключение к Home Assistant: OK. Устройств: %d", len(entities)),
	})
}

func (b *Bot) textHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	tgBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   "Пока я не знаю, как ответить на это. Попробуй /help.",
	})
}