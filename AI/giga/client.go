package giga

import (
	"Antispam/AI"
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
	ctx      context.Context
	client   IGigaClient
	countReq int
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

func (c *Client) GetMessageCharacteristics(msgText string) (*AI.MessageAnalysis, error) {
	err := c.client.AuthWithContext(c.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "auth error")
	}

	if msgText == "" {
		return nil, errors.New("message is not defined")
	}

	logger := slog.Default().With("name", "gigachat")

	req := &gigachat.ChatRequest{
		Model: "GigaChat",
		Messages: []gigachat.Message{
			{
				Role:    "system",
				Content: "Ты — языковая модель, анализирующая сообщения из IT-чата",
			},
			{
				Role:    "user",
				Content: AI.PromptGetSpamPercent(msgText),
			},
		},
		Temperature: AI.Ptr(0.7),
		MaxTokens:   AI.Ptr[int64](200),
	}

	resp, err := c.client.ChatWithContext(c.ctx, req)
	if err != nil {
		return nil, errors.Wrap(err, "request error")
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("response does not contain data")
	}

	var analysis AI.MessageAnalysis
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &analysis); err != nil {
		logger.Error(errors.Wrap(err, "json unmarshal error").Error())
		return nil, err
	}

	if c.countReq%10 == 0 {
		logger.Info(fmt.Sprintf("gigachat API request count: %d", c.countReq+1))
	}

	c.countReq++

	return &analysis, nil
}
