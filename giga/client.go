package giga

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/paulrzcz/go-gigachat"
	"github.com/pkg/errors"
	"log/slog"
)

//go:generate mockgen -source=$GOFILE -destination=./mock/mock.go
type IGigaClient interface {
	AuthWithContext(ctx context.Context) error
	ChatWithContext(ctx context.Context, in *gigachat.ChatRequest) (*gigachat.ChatResponse, error)
}

type Client struct {
	ctx    context.Context
	client IGigaClient
}

func NewGigaClient(ctx context.Context, authKey string) (*Client, error) {
	client, err := gigachat.NewInsecureClientWithAuthKey(authKey)
	if err != nil {
		return nil, errors.Wrap(err, "newGigaClient error")
	}

	return &Client{
		ctx:    ctx,
		client: client,
	}, nil
}

func (c *Client) GetMessageCharacteristics(msgText string) (*MessageAnalysis, error) {
	err := c.client.AuthWithContext(c.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "auth error")
	}

	if msgText == "" {
		return nil, errors.New("message is not defined")
	}

	logger := slog.Default().With("name", "GetSpamPercent")

	req := &gigachat.ChatRequest{
		Model: "GigaChat",
		Messages: []gigachat.Message{
			{
				Role:    "system",
				Content: "Ты — языковая модель, анализирующая сообщения из IT-чата",
			},
			{
				Role:    "user",
				Content: c.promptGetSpamPercent(msgText),
			},
		},
		Temperature: ptr(0.7),
		MaxTokens:   ptr[int64](200),
	}

	resp, err := c.client.ChatWithContext(c.ctx, req)
	if err != nil {
		return nil, errors.Wrap(err, "request error")
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("response does not contain data")
	}

	var analysis MessageAnalysis
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &analysis); err != nil {
		logger.Error(errors.Wrap(err, "json unmarshal error").Error())
		return nil, err
	}

	return &analysis, nil
}

func (c *Client) promptGetSpamPercent(msg string) string {
	return fmt.Sprintf(`По сообщению ты должен определить его характеристики:
								По одному сообщению ты должна определить его характеристики и обосновать выводы.
								
								Определи и выведи результат строго в формате JSON:
								
								- is_spam: Является ли сообщение спамом (true/false)
								- spam_reason: Краткое объяснение, почему сообщение определено (или не определено) как спам
								- is_toxic: Есть ли в сообщении признаки токсичности (true/false)
								- toxic_reason: Краткое объяснение, если сообщение признано токсичным или агрессивным
								- is_offtopic: Является ли сообщение оффтопом (true/false)
								- offtopic_reason: Краткое объяснение, почему сообщение сочтено оффтопом (или нет)
								

								Выведи результат строго в формате JSON без дополнительных комментариев:
								
								{
								  "is_spam": true,
								  "spam_reason": "Сообщение содержит рекламу сомнительного заработка и внешнюю ссылку.",
								  "is_toxic": false,
								  "toxic_reason": "Нет признаков агрессии или оскорблений.",
								  "is_offtopic": true,
								  "offtopic_reason": "Тематика сообщения не связана с IT и не относится к текущему обсуждению."
								}						

								ПЕРЕД ОТВЕТОМ ПРОВЕРЬ JSON НА ВАЛИДНОСТЬ

								Вот сообщение для анализа:
								"%s"`, msg)
}

func ptr[T any](v T) *T {
	return &v
}
