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
	assert.True(t, conf.AI.AIEnabled())
	if assert.NotNil(t, conf.AI.Deepseek) {
		assert.Equal(t, []string{"sk-xxxxxxxxxxxxxxxxxxxxxxxx"}, conf.AI.Deepseek.APIKeys)
	}
	assert.True(t, conf.GitHub.Enabled())
	assert.Equal(t, []string{"ghp_xxx...xxxx", "ghp_yyy...yyyy"}, conf.GitHub.APIKeys)
	assert.Equal(t, "(?i).*([ПPР][OО0][PРR][NHН][OО0]).*|.*([ПPР][NHН][PРR][NHН][OО0]).*", conf.BlockMembers.UserNameRegExp)
}

func Test_AIEnabled(t *testing.T) {
	t.Run("ИИ не задан", func(t *testing.T) {
		assert.False(t, AIConf{}.AIEnabled())
	})
	t.Run("задан без ключей", func(t *testing.T) {
		assert.False(t, AIConf{Deepseek: &Deepseek{}}.AIEnabled())
	})
	t.Run("задан с ключами", func(t *testing.T) {
		assert.True(t, AIConf{Deepseek: &Deepseek{APIKeys: []string{"sk-1"}}}.AIEnabled())
	})
}

func Test_GitHubConf_Enabled(t *testing.T) {
	t.Run("ключи не заданы", func(t *testing.T) {
		assert.False(t, GitHubConf{}.Enabled())
	})
	t.Run("задан один ключ", func(t *testing.T) {
		assert.True(t, GitHubConf{APIKeys: []string{"ghp_1"}}.Enabled())
	})
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
