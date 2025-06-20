package deepseek

import (
	"Antispam/AI"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-deepseek/deepseek"
	"github.com/go-deepseek/deepseek/request"
	"github.com/pkg/errors"
	"log/slog"
	"regexp"
)

//go:generate mockgen -destination=./mock/ds.go github.com/go-deepseek/deepseek Client

type Client struct {
	ctx      context.Context
	client   deepseek.Client
	countReq int
}

func NewDSClient(ctx context.Context, apiKey string) (*Client, error) {
	client, err := deepseek.NewClient(apiKey)
	if err != nil {
		return nil, errors.Wrap(err, "newGigaClient error")
	}

	return &Client{
		ctx:    ctx,
		client: client,
	}, nil
}

func (c *Client) GetMessageCharacteristics(msgText string) (*AI.MessageAnalysis, error) {
	logger := slog.Default().With("name", "deepseek")

	chatReq := &request.ChatCompletionsRequest{
		Model:  deepseek.DEEPSEEK_CHAT_MODEL,
		Stream: false,
		Messages: []*request.Message{
			{
				Role:    "system",
				Content: "Ты — языковая модель, анализирующая сообщения из IT-чата",
			},
			{
				Role:    "user",
				Content: AI.PromptGetSpamPercent(msgText),
			},
		},
	}

	chatResp, err := c.client.CallChatCompletionsChat(c.ctx, chatReq)
	if err != nil {
		return nil, errors.Wrap(err, "CallChatCompletionsChat error")
	}

	if c.countReq%10 == 0 {
		logger.Info(fmt.Sprintf("deepseek API request count: %d", c.countReq+1))
	}
	c.countReq++

	return c.postProcessing(chatResp.Choices[0].Message.Content)
}

func (c *Client) postProcessing(answer string) (*AI.MessageAnalysis, error) {
	var re = regexp.MustCompile("(?s)```json(.*)```")
	if sm := re.FindStringSubmatch(answer); len(sm) > 1 {
		answer = sm[1]
	}

	var analysis AI.MessageAnalysis
	if err := json.Unmarshal([]byte(answer), &analysis); err != nil {
		return nil, errors.Wrap(err, "json unmarshal error")
	}

	return &analysis, nil
}
