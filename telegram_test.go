package app

import (
	mock_app "Antispam/mock"
	"context"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
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

	assert.Equal(t, `{"ID":32323,"Name":"test"}`, u.String())
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
