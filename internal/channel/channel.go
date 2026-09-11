package channel

import "context"

type Channel interface {
	Start(ctx context.Context) error
	Close(ctx context.Context)
	SendMessage(ctx context.Context, chatID int64, text string) (int, error)
	SendNotification(ctx context.Context, text string)
}
