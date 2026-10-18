package deepseek

import (
	mock_deepseek "Antispam/AI/deepseek/mock"
	"context"
	"github.com/go-deepseek/deepseek/response"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_GetMessageCharacteristics(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ds, err := NewDSClient(context.Background(), "123")
	assert.NoError(t, err)

	client := mock_deepseek.NewMockClient(c)
	ds.client = client

	client.EXPECT().CallChatCompletionsChat(gomock.Any(), gomock.Any()).Return(&response.ChatCompletionsResponse{
		Choices: []*response.Choice{
			{
				Message: &response.Message{Content: "```json\n" +
					"{\"is_spam\": false,\n" +
					"	\"spam_reason\": \"Сообщение не содержит рекламы, ссылок или других признаков спама.\",\n" +
					"	\"hate_percent\": 40,\n" +
					"	\"hate_reason\": \"Сообщение содержит угрозу расставания, что может быть воспринято как агрессивное высказывание.\",\n" +
					"	\"is_offtopic\": false,\n" +
					"	\"offtopic_reason\": \"Сообщение касается рабочих отношений, что может быть связано с IT-контекстом.\"\n" +
					"}```"},
			},
		},
	}, nil)

	result, err := ds.GetMessageCharacteristics("test", "")
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.False(t, result.IsSpam)
		assert.Equal(t, "Сообщение не содержит рекламы, ссылок или других признаков спама.", result.SpamReason)
		assert.Equal(t, 40, result.HatePercent)
		assert.Equal(t, "Сообщение содержит угрозу расставания, что может быть воспринято как агрессивное высказывание.", result.HateReason)
	}
}

func Test_GetMessageCharacteristics_emptyResponse(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ds, err := NewDSClient(context.Background(), "123")
	assert.NoError(t, err)

	client := mock_deepseek.NewMockClient(c)
	ds.client = client

	client.EXPECT().CallChatCompletionsChat(gomock.Any(), gomock.Any()).Return(&response.ChatCompletionsResponse{}, nil)

	result, err := ds.GetMessageCharacteristics("test", "")
	assert.Nil(t, result)
	assert.EqualError(t, err, "response does not contain data")
}

func Test_GetMessageCharacteristics_apiError(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ds, err := NewDSClient(context.Background(), "123")
	assert.NoError(t, err)

	client := mock_deepseek.NewMockClient(c)
	ds.client = client

	client.EXPECT().CallChatCompletionsChat(gomock.Any(), gomock.Any()).Return(nil, errors.New("402 insufficient balance"))

	result, err := ds.GetMessageCharacteristics("test", "")
	assert.Nil(t, result)
	assert.EqualError(t, err, "CallChatCompletionsChat error: 402 insufficient balance")
}

func Test_GetMessageCharacteristics_invalidJSON(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ds, err := NewDSClient(context.Background(), "123")
	assert.NoError(t, err)

	client := mock_deepseek.NewMockClient(c)
	ds.client = client

	client.EXPECT().CallChatCompletionsChat(gomock.Any(), gomock.Any()).Return(&response.ChatCompletionsResponse{
		Choices: []*response.Choice{
			{Message: &response.Message{Content: "не json, а обычный текст"}},
		},
	}, nil)

	result, err := ds.GetMessageCharacteristics("test", "")
	assert.Nil(t, result)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "json unmarshal error")
		assert.Contains(t, err.Error(), "не json, а обычный текст")
	}
}
