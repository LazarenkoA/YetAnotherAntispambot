package deepseek

import (
	mock_deepseek "Antispam/AI/deepseek/mock"
	"context"
	"github.com/go-deepseek/deepseek/response"
	"github.com/golang/mock/gomock"
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

	result, err := ds.GetMessageCharacteristics("test")
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.False(t, result.IsSpam)
		assert.Equal(t, "Сообщение не содержит рекламы, ссылок или других признаков спама.", result.SpamReason)
		assert.Equal(t, 40, result.HatePercent)
		assert.Equal(t, "Сообщение содержит угрозу расставания, что может быть воспринято как агрессивное высказывание.", result.HateReason)
	}
}
