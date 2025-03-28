package app

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

	Question     question  `yaml:"question"`
	Answers      []*answer `yaml:"answers"`
	CountVoted   int       `yaml:"countVoted"`
	BlockMembers struct {
		UserNameRegExp string `yaml:"userNameRegExp"`
	} `yaml:"blockMembers"`
	AI struct {
		GigaChat struct {
			ClientID     string `yaml:"clientID"`
			ClientSecret string `yaml:"clientSecret"`
			AuthKey      string `yaml:"authKey"`
		} `yaml:"gigachat"`
	} `yaml:"ai"`
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
	if result.CountVoted == 0 {
		result.CountVoted = 5
	}
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
    correct: true
countVoted: 10 # количество проголосовавщих за бан. По умолчанию 5

blockMembers: # не обязательная настройка, можно задать регулярное выражение по которому будет баниться никнейм или ФИО. Бан будет сразу при вступлении, без вопроса
  userNameRegExp: "(?i).*(PORNO|ПОРНО).*" # проверка будет выполняться по полям UserName, FirstName, LastName

ai: # если настройка задана антиспам будет аанализировать первое отправвленое сообщение от пользователя на предмет спам - не спам
  gigachat:
    authKey: <ключ можно получить https://developers.sber.ru>
`
}
