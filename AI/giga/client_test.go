package giga

import (
	"Antispam/AI/giga/mock"
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/paulrzcz/go-gigachat"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func Test_GetCommitMsg(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	// test := iter.Seq[string](func(yield func(string) bool) {
	// 	yield("1")
	// 	yield("2")
	// 	yield("3")
	// 	yield("4")
	// })

	//n, s := iter.Pull[string](test)

	// test := func(yield func(str string) bool) {
	// 	yield("re")
	// 	yield("куку")
	// }

	// for v := range test {
	// 	fmt.Println(v)
	// }

	t.Run("error create", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return nil, errors.New("error")
		})
		defer p.Reset()

		cli, err := NewGigaClient(context.Background(), "111")
		assert.Nil(t, cli)
		assert.EqualError(t, err, "newGigaClient error: error")
	})
	t.Run("auth error", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(errors.New("error"))

		cli, _ := NewGigaClient(context.Background(), "111")
		cli.client = client

		_, err := cli.GetMessageCharacteristics("")
		assert.EqualError(t, err, "auth error: error")
	})
	t.Run("req error", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(nil, errors.New("error"))

		cli, _ := NewGigaClient(context.Background(), "111")
		cli.client = client

		_, err := cli.GetMessageCharacteristics("tyuyu")
		assert.EqualError(t, err, "request error: error")
	})
	t.Run("response does not contain data", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(&gigachat.ChatResponse{}, nil)

		cli, _ := NewGigaClient(context.Background(), "111")
		cli.client = client

		_, err := cli.GetMessageCharacteristics("ghgh")
		assert.EqualError(t, err, "response does not contain data")
	})
	t.Run("diff is not defined", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)

		cli, _ := NewGigaClient(context.Background(), "111")
		cli.client = client

		_, err := cli.GetMessageCharacteristics("")
		assert.EqualError(t, err, "message is not defined")
	})
	t.Run("pass", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClientWithAuthKey, func(authKey string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(&gigachat.ChatResponse{
			Choices: []gigachat.Choice{{Message: gigachat.Message{Content: `
								{
								  "is_spam": true,
								  "spam_reason": "Сообщение содержит рекламу сомнительного заработка и внешнюю ссылку",
								  "is_toxic": false,
								  "toxic_reason": "Нет признаков агрессии или оскорблений.",
								  "is_offtopic": true,
								  "offtopic_reason": "Тематика сообщения не связана с IT и не относится к текущему обсуждению."
								}`}}},
		}, nil)

		cli, _ := NewGigaClient(context.Background(), "==")
		cli.client = client

		analysis, err := cli.GetMessageCharacteristics("hjhj")
		assert.NoError(t, err)

		if assert.True(t, analysis.IsSpam) {
			assert.Equal(t, "Сообщение содержит рекламу сомнительного заработка и внешнюю ссылку", analysis.SpamReason)
		}
	})
}
