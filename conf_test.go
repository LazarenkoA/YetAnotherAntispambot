package app

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
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
	assert.Equal(t, "122323", conf.AI.GigaChat.ClientID)
	assert.Equal(t, "3fffff", conf.AI.GigaChat.ClientSecret)
}
