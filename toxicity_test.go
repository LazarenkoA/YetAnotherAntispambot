package app

import (
	"Antispam/AI"
	mock_app "Antispam/mock"
	"context"
	"encoding/json"
	"github.com/go-telegram/bot/models"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"log/slog"
	"testing"
)

func Test_ToxicityStat_Percent(t *testing.T) {
	t.Run("нет данных", func(t *testing.T) {
		assert.Equal(t, float64(0), ToxicityStat{}.Percent())
	})
	t.Run("все токсичные", func(t *testing.T) {
		assert.Equal(t, float64(100), ToxicityStat{Total: 5, Toxic: 5}.Percent())
	})
	t.Run("нет токсичных", func(t *testing.T) {
		assert.Equal(t, float64(0), ToxicityStat{Total: 7, Toxic: 0}.Percent())
	})
	t.Run("округление", func(t *testing.T) {
		// 1 из 3 = 33.33% -> 33
		assert.Equal(t, float64(33), ToxicityStat{Total: 3, Toxic: 1}.Percent())
	})
	t.Run("отрицательный total", func(t *testing.T) {
		assert.Equal(t, float64(0), ToxicityStat{Total: -1, Toxic: 1}.Percent())
	})
}

func Test_RecordToxicity(t *testing.T) {
	t.Run("токсичное сообщение учитывается", func(t *testing.T) {
		wd := &Telega{logger: slog.Default()}
		stat := wd.RecordToxicity(1, 2, hateToxicThreshold)
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 1}, stat)
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 1}, wd.GetToxicity(1, 2))
	})
	t.Run("порог токсичности", func(t *testing.T) {
		wd := &Telega{logger: slog.Default()}
		wd.RecordToxicity(1, 2, hateToxicThreshold-1)
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 0}, wd.GetToxicity(1, 2))

		wd.RecordToxicity(1, 2, hateToxicThreshold)
		assert.Equal(t, ToxicityStat{Total: 2, Toxic: 1}, wd.GetToxicity(1, 2))
	})
	t.Run("нетоксичные сообщения не увеличивают токсичность", func(t *testing.T) {
		wd := &Telega{logger: slog.Default()}
		for range 5 {
			wd.RecordToxicity(1, 2, 0)
		}
		wd.RecordToxicity(1, 2, hateWarnThreshold)

		stat := wd.GetToxicity(1, 2)
		assert.Equal(t, 6, stat.Total)
		assert.Equal(t, 0, stat.Toxic)
		assert.Equal(t, float64(0), stat.Percent())
	})
	t.Run("статистика изолирована по чатам и пользователям", func(t *testing.T) {
		wd := &Telega{logger: slog.Default()}
		wd.RecordToxicity(1, 2, hateToxicThreshold)
		wd.RecordToxicity(1, 3, 0)
		wd.RecordToxicity(2, 2, 0)

		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 1}, wd.GetToxicity(1, 2))
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 0}, wd.GetToxicity(1, 3))
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 0}, wd.GetToxicity(2, 2))
		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(3, 3))
	})
	t.Run("nil карта инициализируется", func(t *testing.T) {
		wd := &Telega{logger: slog.Default()}
		assert.NotPanics(t, func() {
			wd.RecordToxicity(1, 2, 10)
		})
	})
}

func Test_formatToxicReply(t *testing.T) {
	stat := ToxicityStat{Total: 10, Toxic: 4}
	txt := formatToxicReply(&AI.MessageAnalysis{
		HatePercent: 92,
		HateReason:  "оскорбления",
	}, stat)

	assert.Contains(t, txt, "токсичность сообщения: 92%")
	assert.Contains(t, txt, "оскорбления")
	assert.Contains(t, txt, "40%")
	assert.Contains(t, txt, "4 токсичных из 10 проверенных")
}

func Test_store_restoreToxicityInfo(t *testing.T) {
	t.Run("сохранение и восстановление", func(t *testing.T) {
		c := gomock.NewController(t)
		defer c.Finish()

		mockRedis := mock_app.NewMockIRedis(c)
		wd := &Telega{r: mockRedis, logger: slog.Default()}

		wd.RecordToxicity(1, 2, hateToxicThreshold)
		wd.RecordToxicity(1, 2, 0)

		expected, err := json.Marshal(wd.toxicity)
		assert.NoError(t, err)

		mockRedis.EXPECT().Set(toxicityStatsKey, string(expected), gomock.Any()).Return(nil)
		wd.storeToxicityInfo()

		// новая инстанс, восстанавливаем
		wd2 := &Telega{r: mockRedis, logger: slog.Default()}
		mockRedis.EXPECT().Get(toxicityStatsKey).Return(string(expected), nil)
		wd2.restoreToxicityInfo()

		assert.Equal(t, ToxicityStat{Total: 2, Toxic: 1}, wd2.GetToxicity(1, 2))
	})
	t.Run("статистики еще нет - начинаем с нуля", func(t *testing.T) {
		c := gomock.NewController(t)
		defer c.Finish()

		mockRedis := mock_app.NewMockIRedis(c)
		wd := &Telega{r: mockRedis, logger: slog.Default()}

		mockRedis.EXPECT().Get(toxicityStatsKey).Return("", errors.New("redis: nil"))
		wd.restoreToxicityInfo()

		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(1, 2))
	})
	t.Run("битый json - не падаем", func(t *testing.T) {
		c := gomock.NewController(t)
		defer c.Finish()

		mockRedis := mock_app.NewMockIRedis(c)
		wd := &Telega{r: mockRedis, logger: slog.Default()}

		mockRedis.EXPECT().Get(toxicityStatsKey).Return("not a json", nil)
		wd.restoreToxicityInfo()

		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(1, 2))
	})
}

// Test_CheckMessageModeration проверяет обработку сообщений ИИ-модерацией и учет статистики токсичности
func Test_CheckMessageModeration(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ctx := context.Background()
	conf := &Conf{AI: AIConf{Deepseek: &Deepseek{APIKeys: []string{"key"}}}}

	newMsg := func() *models.Message {
		return &models.Message{
			ID:   42,
			Text: "test message",
			Chat: models.Chat{ID: 100},
			From: &models.User{ID: 200, Username: "user"},
		}
	}

	// перехватываем отправку ответов через replyFn (шов для тестов)
	var sentMessages []string
	captureReply := func(wd *Telega) {
		wd.replyFn = func(msg string, imgURL string, chatID int64, buttons Buttons, parentMessageID int) (*models.Message, error) {
			sentMessages = append(sentMessages, msg)
			return &models.Message{ID: parentMessageID + 1}, nil
		}
	}

	newTelega := func(client *mock_app.MockIMessageAnalysis) *Telega {
		wd := &Telega{logger: slog.Default(), ctx: ctx}
		wd.pool.Store(int64(100), []IMessageAnalysis{client})
		captureReply(wd)
		return wd
	}

	t.Run("ИИ не настроен - ничего не происходит", func(t *testing.T) {
		client := mock_app.NewMockIMessageAnalysis(c)
		wd := newTelega(client)

		wd.CheckMessageModeration(newMsg(), &Conf{})

		assert.Empty(t, sentMessages)
		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(100, 200))
	})
	t.Run("ошибка ИИ - статистика не меняется", func(t *testing.T) {
		client := mock_app.NewMockIMessageAnalysis(c)
		client.EXPECT().GetMessageCharacteristics("test message", gomock.Any()).Return(nil, errors.New("insufficient balance"))
		wd := newTelega(client)

		wd.CheckMessageModeration(newMsg(), conf)

		// API недоступен - ни ответа, ни записи в статистику
		assert.Empty(t, sentMessages)
		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(100, 200))
	})
	t.Run("токсичное сообщение - ответ с процентами и запись в статистику", func(t *testing.T) {
		client := mock_app.NewMockIMessageAnalysis(c)
		client.EXPECT().GetMessageCharacteristics("test message", gomock.Any()).Return(&AI.MessageAnalysis{
			HatePercent: 90,
			HateReason:  "оскорбления",
		}, nil)
		wd := newTelega(client)

		wd.CheckMessageModeration(newMsg(), conf)

		if assert.Len(t, sentMessages, 1) {
			assert.Contains(t, sentMessages[0], "токсичность сообщения: 90%")
			assert.Contains(t, sentMessages[0], "оскорбления")
			assert.Contains(t, sentMessages[0], "100%")
			assert.Contains(t, sentMessages[0], "1 токсичных из 1 проверенных")
		}
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 1}, wd.GetToxicity(100, 200))
	})
	t.Run("не токсичное сообщение - статистика считается, ответа нет", func(t *testing.T) {
		sentMessages = nil
		client := mock_app.NewMockIMessageAnalysis(c)
		client.EXPECT().GetMessageCharacteristics("test message", gomock.Any()).Return(&AI.MessageAnalysis{
			HatePercent: hateWarnThreshold,
			HateReason:  "резкость",
		}, nil)
		wd := newTelega(client)

		wd.CheckMessageModeration(newMsg(), conf)

		assert.Empty(t, sentMessages)
		assert.Equal(t, ToxicityStat{Total: 1, Toxic: 0}, wd.GetToxicity(100, 200))
	})
	t.Run("nil сообщение - ничего не происходит", func(t *testing.T) {
		client := mock_app.NewMockIMessageAnalysis(c)
		wd := newTelega(client)

		assert.NotPanics(t, func() {
			wd.CheckMessageModeration(nil, conf)
		})
		assert.Equal(t, ToxicityStat{}, wd.GetToxicity(100, 200))
	})
	t.Run("накопление статистики по одному пользователю", func(t *testing.T) {
		sentMessages = nil
		client := mock_app.NewMockIMessageAnalysis(c)
		wd := newTelega(client)

		gomock.InOrder(
			client.EXPECT().GetMessageCharacteristics(gomock.Any(), gomock.Any()).Return(&AI.MessageAnalysis{HatePercent: 10}, nil),
			client.EXPECT().GetMessageCharacteristics(gomock.Any(), gomock.Any()).Return(&AI.MessageAnalysis{HatePercent: 86, HateReason: "r"}, nil),
			client.EXPECT().GetMessageCharacteristics(gomock.Any(), gomock.Any()).Return(&AI.MessageAnalysis{HatePercent: 0}, nil),
		)

		for range 3 {
			wd.CheckMessageModeration(newMsg(), conf)
		}

		stat := wd.GetToxicity(100, 200)
		assert.Equal(t, 3, stat.Total)
		assert.Equal(t, 1, stat.Toxic)
		assert.Equal(t, float64(33), stat.Percent())

		// в ответе на токсичное сообщение указан накопленный на момент ответа процент
		if assert.Len(t, sentMessages, 1) {
			assert.Contains(t, sentMessages[0], "токсичность сообщения: 86%")
			assert.Contains(t, sentMessages[0], "50% (1 токсичных из 2 проверенных)")
		}
	})
}
