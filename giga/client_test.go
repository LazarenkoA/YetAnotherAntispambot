package giga

import (
	"context"
	"testing"

	mock_giga "github.com/LazarenkoA/GigaCommits/giga/mock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/paulrzcz/go-gigachat"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func Test_prompt(t *testing.T) {
	prompt := new(Client).prompt()
	assert.Equal(t, `Ты модератор IT чата. В ЧАТА ЗАПРЕЩЕН ПОИСК РАБОТЫ И НАЕМ СОТРУДНИКОВ. Зашел новый участник и отправил новое сообщение, произведи анализ сообщения из чата и оцени вероятность того, что оно является спамом.
Верни число в процентах (от 0 до 100), где 0 означает, что сообщение определенно не является спамом, а 100 означает, что сообщение определенно является спамом.
Ответ должен соответствовать такому шаблону: <int: вероятность того что это спам>|<string: пояснение почему ты считаешь это спамом>
например 89|в сообщении фигурирует фраза про криптовалюту и заработок
Вот сообщение:`, prompt)
}

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
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return nil, errors.New("error")
		})
		defer p.Reset()

		cli, err := NewGigaClient(context.Background(), "111", "222")
		assert.Nil(t, cli)
		assert.EqualError(t, err, "newGigaClient error: error")
	})
	t.Run("auth error", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(errors.New("error"))

		cli, _ := NewGigaClient(context.Background(), "111", "222")
		cli.client = client

		_, _, _, err := cli.GetSpamPercent("")
		assert.EqualError(t, err, "auth error: error")
	})
	t.Run("req error", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(nil, errors.New("error"))

		cli, _ := NewGigaClient(context.Background(), "111", "222")
		cli.client = client

		_, _, _, err := cli.GetSpamPercent("tyuyu")
		assert.EqualError(t, err, "request error: error")
	})
	t.Run("response does not contain data", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(&gigachat.ChatResponse{}, nil)

		cli, _ := NewGigaClient(context.Background(), "111", "222")
		cli.client = client

		_, _, _, err := cli.GetSpamPercent("ghgh")
		assert.EqualError(t, err, "response does not contain data")
	})
	t.Run("diff is not defined", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)

		cli, _ := NewGigaClient(context.Background(), "111", "222")
		cli.client = client

		_, _, _, err := cli.GetSpamPercent("")
		assert.EqualError(t, err, "message is not defined")
	})
	t.Run("pass", func(t *testing.T) {
		p := gomonkey.ApplyFunc(gigachat.NewInsecureClient, func(clientId string, clientSecret string) (*gigachat.Client, error) {
			return new(gigachat.Client), nil
		})
		defer p.Reset()

		client := mock_giga.NewMockIGigaClient(c)
		client.EXPECT().AuthWithContext(gomock.Any()).Return(nil)
		client.EXPECT().ChatWithContext(gomock.Any(), gomock.Any()).Return(&gigachat.ChatResponse{
			Choices: []gigachat.Choice{{Message: gigachat.Message{Content: "89|в сообщении фигурирует фраза про криптовалюту и заработок"}}},
		}, nil)

		cli, _ := NewGigaClient(context.Background(), "111", "222")
		cli.client = client

		s, perc, r, err := cli.GetSpamPercent("hjhj")
		assert.NoError(t, err)
		assert.Equal(t, "в сообщении фигурирует фраза про криптовалюту и заработок", r)
		assert.True(t, s)
		assert.Equal(t, perc, 89)
	})
}
