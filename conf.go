package main

import (
	dry "github.com/ungerik/go-dry"
	"gopkg.in/yaml.v2"
)

type answer struct {
	Txt     string `yaml:"txt"`
	Correct bool   `yaml:"correct"`
}

type question struct {
	Txt string `yaml:"txt"`
	Img string `yaml:"img"`
}

type Conf struct {
	// Обратный отчет в секундах
	Timeout     int    `yaml:"timeout"`
	KickCaption string `yaml:"kickCaption"`

	Question question  `yaml:"question"`
	Answers  []*answer `yaml:"answers"`
}

func LoadConfFromFile(confpath string) (result *Conf, err error) {
	if b, err := dry.FileGetBytes(confpath); err == nil {
		return LoadConf(b)
	} else {
		return nil, err
	}
}

func LoadConf(conf []byte) (result *Conf, err error) {
	result = new(Conf)
	err = yaml.Unmarshal(conf, &result)
	return result, err
}

func confExample() string {
	return `timeout: 60 # время на ответ в секундах, не обязательный параметр, по дефолту 60 секунд
kickCaption: "Я пожалуй пойду" # Заголовок кнопки с обратным отсчетом, не обязательный параметр, по дефолту заголовок "не знаю"
question:
  txt: "Что вы видите на картинке?"
  img: "https://i.imgur.com/UUMAx2Zm.jpg" # не обязательный параметр
answers:
  - txt: "Бабочку"
    correct: true
  - txt: "Цветы"
  - txt: "Лицо"
    correct: true`
}
