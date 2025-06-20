package app

import (
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	"os"
	"testing"
	"time"
)

func Test_Conf(t *testing.T) {
	confTxt := confExample()
	f, _ := os.CreateTemp("", "")
	f.WriteString(confTxt)
	f.Close()

	defer os.Remove(f.Name())

	conf, _ := LoadConfFromFile(f.Name())
	assert.Equal(t, 10, conf.CountVoted)
	assert.Equal(t, 60, conf.Timeout)
	assert.Equal(t, "Я пожалуй пойду", conf.KickCaption)
	assert.Equal(t, 3, len(conf.Answers))
	assert.Equal(t, "Что вы видите на картинке?", conf.Question.Txt)
	assert.Len(t, conf.AI, 2)
	assert.Equal(t, "(?i).*([ПPР][OО0][PРR][NHН][OО0]).*|.*([ПPР][NHН][PРR][NHН][OО0]).*", conf.BlockMembers.UserNameRegExp)
}

func Test_rate_limiter(t *testing.T) {
	s := rate.Sometimes{Interval: time.Millisecond * 50}
	count := 0

	for range 100 {
		s.Do(func() { count++ })
		time.Sleep(time.Millisecond * 10)
	}

	assert.Equal(t, 20, count)
}
