package giga

import (
	"context"
	"fmt"
	"github.com/paulrzcz/go-gigachat"
	"github.com/pkg/errors"
	"log"
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

func NewGigaClient(ctx context.Context, clientId, clientSecret string) (*Client, error) {
	client, err := gigachat.NewInsecureClient(clientId, clientSecret)
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

	req := &gigachat.ChatRequest{
		Model: "GigaChat",
		Messages: []gigachat.Message{
			{
				Role:    "system",
				Content: c.prompt(),
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
	if len(parts) != 3 {
		log.Println("bad response format: ", resp.Choices[0].Message.Content)
		return false, -1, "", errors.New("bad response format")
	}

	solution, err := strconv.ParseBool(strings.TrimSpace(parts[0]))
	if err != nil {
		log.Println("parse bool, bad response format: ", resp.Choices[0].Message.Content)
		return false, -1, "", errors.New("bad response format")
	}

	percent, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		log.Println("parse int, bad response format: ", resp.Choices[0].Message.Content)
		return false, -1, "", errors.New("bad response format")
	}

	return solution, percent, strings.TrimSpace(parts[2]), nil
}

func (c *Client) prompt() string {
	return fmt.Sprintf("Ты модератор IT чата. В чате запрещен наем сотрудников. Зашел новый участник и отправил новое сообщение, произведи анализ сообщения из чата и оцени вероятность того, что оно является спамом.\n" +
		"Верни число в процентах (от 0 до 100), где 0 означает, что сообщение определенно не является спамом, а 100 означает, что сообщение определенно является спамом.\n" +
		"Ответ должен соответствовать такому шаблону: <bool: спам или не спам>|<int: вероятность того что это спам>|<string: пояснение почему ты считаешь это спамом>\n" +
		"например true|89|в сообщении фигурирует фраза про криптовалюту и заработок\n" +
		"Вот сообщение:")
}

func ptr[T any](v T) *T {
	return &v
}
