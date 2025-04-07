package giga

import (
	"context"
	"fmt"
	"github.com/paulrzcz/go-gigachat"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"log/slog"
	"strconv"
	"strings"
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

func (c *Client) GetSpamPercent(msgText string) (bool, int, string, error) {
	err := c.client.AuthWithContext(c.ctx)
	if err != nil {
		return false, -1, "", errors.Wrap(err, "auth error")
	}

	if msgText == "" {
		return false, -1, "", errors.New("message is not defined")
	}

	logger := slog.Default().With("name", "GetSpamPercent")

	req := &gigachat.ChatRequest{
		Model: "GigaChat",
		Messages: []gigachat.Message{
			{
				Role:    "system",
				Content: c.promptGetSpamPercent(),
			},
			{
				Role:    "user",
				Content: msgText,
			},
		},
		Temperature: ptr(0.7),
		MaxTokens:   ptr[int64](200),
	}

	resp, err := c.client.ChatWithContext(c.ctx, req)
	if err != nil {
		return false, -1, "", errors.Wrap(err, "request error")
	}

	if len(resp.Choices) == 0 {
		return false, -1, "", errors.New("response does not contain data")
	}

	parts := strings.Split(resp.Choices[0].Message.Content, "|")
	if len(parts) != 2 {
		logger.Error("bad response format, parts not 2", "content", resp.Choices[0].Message.Content)
		return false, -1, "", errors.New("bad response format")
	}

	percent, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		logger.Error("parse int, bad response format", "content", resp.Choices[0].Message.Content)
		return false, -1, "", errors.New("bad response format")
	}

	isSpam := percent >= 70
	return isSpam, percent, strings.TrimSpace(parts[1]), nil
}

func (c *Client) promptGetSpamPercent() string {
	return fmt.Sprintf("Ты модератор IT чата. В ЧАТА ЗАПРЕЩЕН ПОИСК РАБОТЫ И НАЕМ СОТРУДНИКОВ. Зашел новый участник и отправил новое сообщение, произведи анализ сообщения из чата и оцени вероятность того, что оно является спамом.\n" +
		"Верни число в процентах (от 0 до 100), где 0 означает, что сообщение определенно не является спамом, а 100 означает, что сообщение определенно является спамом.\n" +
		"Ответ должен соответствовать такому шаблону: <int: вероятность того что это спам>|<string: пояснение почему ты считаешь это спамом>\n" +
		"например 89|в сообщении фигурирует фраза про криптовалюту и заработок\n" +
		"перед тем как отправить ответ, ПЕРЕПРОВЕРЬ правильно ли ты понял мою просьбу и верен ли твой ответ.\n" +
		"Вот сообщение:")
}

func (c *Client) promptCheck(percent int) string {
	return fmt.Sprintf("LLM был задан вопрос определить является ли данное сообщение спамом ее ответ %q."+
		"Верен или нет ее ответ, ответь \"да\" если LLM дала верный ответ и \"нет\" если не верен", lo.If(percent >= 70, "является спамом").Else("не является спамом"))
}

func ptr[T any](v T) *T {
	return &v
}
