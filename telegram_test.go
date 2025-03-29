package app

import (
	mock_app "Antispam/mock"
	"context"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"math"
	"testing"
	"time"
)

func Test_KilledInfo(t *testing.T) {
	tmpTime, _ := time.Parse(time.DateTime, "2025-03-10 11:46:10")
	k := &KilledInfo{
		UserID: 32323,
		To:     tmpTime,
	}

	assert.Equal(t, `{"UserID":32323,"UserName":"","To":"2025-03-10T11:46:10Z"}`, k.String())
}

func Test_UserInfo(t *testing.T) {
	u := &UserInfo{
		ID:   32323,
		Name: "test",
	}

	assert.Equal(t, `{"ID":32323,"Name":"test","Weight":0}`, u.String())
}

func Test_watchKilledUsers(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockRedis := mock_app.NewMockIRedis(c)

	telega := &Telega{ctx: ctx, cancel: cancel, r: mockRedis}

	tmp := (&KilledInfo{UserName: "test", To: time.Now().Add(time.Hour * -2)}).String()
	mockRedis.EXPECT().Items(killedUsers).Return([]string{tmp}, nil)
	mockRedis.EXPECT().DeleteItems(killedUsers, tmp)
	telega.watchKilledUsers(time.Second)
}

func Test_SaveMember(t *testing.T) {
	telega := &Telega{users: map[int64]map[int64]UserInfo{}}

	telega.SaveMember(111, &tgbotapi.User{
		ID:       21212,
		UserName: "test",
	})
	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})

	assert.Equal(t, 1, len(telega.users))
	assert.Equal(t, 2, len(telega.users[111]))
	assert.Equal(t, 1, int(telega.users[111][323333].Weight))
	assert.Equal(t, 1, int(telega.users[111][21212].Weight))

	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})
	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})

	assert.Equal(t, 3, int(telega.users[111][323333].Weight))
	assert.Equal(t, 1, int(telega.users[111][21212].Weight))
}

func Test_GetRandUser(t *testing.T) {
	telega := &Telega{users: map[int64]map[int64]UserInfo{
		000: {111: UserInfo{ID: 111}},
	}}

	v := telega.GetRandUser(111, 0)
	assert.Nil(t, v)

	v = telega.GetRandUser(000, 0)
	if assert.NotNil(t, v) {
		assert.Equal(t, int64(111), v.ID)
	}

	telega.users[000][222] = UserInfo{ID: 222}

	assert.Equal(t, 2, len(telega.users[000]))

	v = telega.GetRandUser(000, 0)
	if assert.NotNil(t, v) {
		assert.True(t, v.ID == 111 || v.ID == 222)
	}
}

func Test_GetRandUserByWeight(t *testing.T) {
	t.Run("test1", func(t *testing.T) {
		telega := &Telega{users: map[int64]map[int64]UserInfo{
			000: {
				111: UserInfo{ID: 111, Weight: 0},
				222: UserInfo{ID: 222, Weight: 1},
				333: UserInfo{ID: 333, Weight: 2},
			},
		}}

		check := map[int64]float32{}
		for range 1000 {
			user := telega.GetRandUserByWeight(000, 0)
			check[user.ID]++
		}

		assert.Equal(t, float64(0), roundToTens(float64(check[111]/1000)*100))
		assert.Equal(t, float64(30), roundToTens(float64(check[222]/1000)*100))
		assert.Equal(t, float64(70), roundToTens(float64(check[333]/1000)*100))
	})
	t.Run("test2", func(t *testing.T) {
		telega := &Telega{users: map[int64]map[int64]UserInfo{
			000: {
				111: UserInfo{ID: 111, Weight: 0},
				222: UserInfo{ID: 222, Weight: 0},
				333: UserInfo{ID: 333, Weight: 0},
			},
		}}

		check := map[int64]float32{}
		for range 1000 {
			user := telega.GetRandUserByWeight(000, 0)
			check[user.ID]++
		}

		assert.Equal(t, float64(30), roundToTens(float64(check[111]/1000)*100))
		assert.Equal(t, float64(30), roundToTens(float64(check[222]/1000)*100))
		assert.Equal(t, float64(30), roundToTens(float64(check[333]/1000)*100))
	})
}

func roundToTens(n float64) float64 {
	return math.Round(n/10) * 10
}
