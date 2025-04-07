package app

import (
	"encoding/json"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/pkg/errors"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

func (wd *Telega) checkAI(chatID int64, msg *tgbotapi.Message) {
	// пример команды
	// /checkAI::Здраствуйте!  Нужны люди на удалённый проект, от 500$ в неделю, 2-3 часа в день, 18+. Жду в личке::MTZmODdlMmU...xLTM0OWE0MmE1NDJiNg==
	conf := readConf(wd, chatID)
	authKey := ""

	if conf != nil {
		authKey = conf.AI.GigaChat.AuthKey
	}

	split := strings.Split(msg.Text, "::")
	if len(split) >= 3 {
		authKey = strings.TrimSpace(split[2])
	} else if len(split) < 2 {
		wd.SendMsg("Не корректный формат сообщения", "", chatID, Buttons{})
		return
	}

	if authKey == "" {
		wd.SendMsg("Не определен authKey для giga chat", "", chatID, Buttons{})
		return
	}

	isSpam, percent, reason, err := wd.gigaClient(authKey).GetSpamPercent(split[1])
	if err != nil {
		wd.SendMsg(fmt.Sprintf("Произошла ошибка: %s", err.Error()), "", chatID, Buttons{})
	} else {
		wd.SendMsg(fmt.Sprintf("%v, %v, %s", isSpam, percent, reason), "", chatID, Buttons{})
	}
}

func (wd *Telega) exampleConf(chatID int64) {
	if f, err := os.CreateTemp("", "*.yaml"); err == nil {
		f.WriteString(confExample())
		f.Close()

		wd.SendFile(chatID, f.Name())
		os.RemoveAll(f.Name())
	}
}

func (wd *Telega) help(chatID int64) {
	me, _ := wd.bot.GetMe()

	msg := fmt.Sprintf("Антиспам для групп, при входе нового участника в группу бот задает вопрос, если ответа нет, участник блокируется.\n"+
		"Если нужно заблокировать пользователя тегните сообщения ботом (ответить на сообщение с текстом @%s).", me.UserName)
	wd.SendMsg(msg, "", chatID, Buttons{})
}

func (wd *Telega) configuration(chatID int64, update tgbotapi.Update) {
	key := strconv.FormatInt(wd.GetMessage(update).From.ID, 10)
	if wd.r.KeyExists(key) {
		configuration(wd, update, chatID)
	} else {
		wd.SendMsg("Для вас не найден активный чат, видимо вы не добавили бота в чат.", "", chatID, Buttons{})
	}
}

func (wd *Telega) start(chatID int64, msg *tgbotapi.Message) {
	txt := fmt.Sprintf("Привет %s %s\n"+
		"Что бы начать пользоваться ботом нужно выполнить следующие действия:\n"+
		"1. Добавить бота в нужную группу\n"+
		"2. Выдать боту админские права\n"+
		"3. Выполнить в боте команду /configuration, выбрать чат и загрузить (отправить файл) конфиг. "+
		"Пример конфига можно скачать выполнив команду /exampleconf", msg.From.FirstName, msg.From.LastName)
	wd.SendMsg(txt, "", chatID, Buttons{})
}

func (wd *Telega) randomModerator(chatID int64, msg *tgbotapi.Message, deadline time.Time) {
	if userName, deadline := wd.GetActiveRandModerator(chatID); userName != "" {
		wd.SendTTLMsg(fmt.Sprintf("%s уже выбран модератором, перевыбрать можно после %s", userName, deadline.Format("02-01-2006 15:04:05")), "", chatID, Buttons{}, time.Second*5)
		return
	}

	randUser := wd.GetRandUserByWeight(chatID, msg.From.ID)
	if randUser == nil {
		wd.SendTTLMsg("Не смог получить кандидата", "", chatID, Buttons{}, time.Second*5)
		return
	}

	if wd.UserIsAdmin(msg.Chat.ChatConfig(), randUser.ID) {
		wd.SendTTLMsg(fmt.Sprintf("%s уже является администратором, можно попробовать повторно выбрать кандидатуру", randUser.Name), "", chatID, Buttons{}, time.Second*5)
		return
	}

	if err := wd.AppointModerator(chatID, randUser, deadline); err != nil {
		wd.SendTTLMsg(fmt.Sprintf("Произошла ошибка: %v", err.Error()), "", chatID, Buttons{}, time.Second*5)
	} else {
		wd.SendMsg(fmt.Sprintf("У нас новый модератор (%s), срок до %v", randUser.Name, deadline.Format("02-01-2006 15:04")), "", chatID, Buttons{})
	}
}

func (wd *Telega) randomModeratorAutoExtend(chatID int64, msg *tgbotapi.Message) {
	if wd.randomModeratorMX.TryLock() {
		defer wd.randomModeratorMX.Unlock()
	} else {
		return
	}

	for {
		deadline := time.Now().Add(time.Hour * 24)
		wd.randomModerator(chatID, msg, deadline)

		select {
		case <-wd.ctx.Done():
			return
		case <-time.After((time.Minute * 1440) + 1): // 24ч +1 минута
		}
	}
}

func (wd *Telega) russianRouletteKilled(chatID int64) {
	killed, err := wd.r.Items(killedUsers)
	if err != nil {
		log.Println(errors.Wrap(err, "redis read error"))
		return
	}

	viewKilled := make([]string, 0, len(killed))
	for _, data := range killed {
		tmp := new(KilledInfo)
		if err := json.Unmarshal([]byte(data), tmp); err == nil && tmp.UserName != "" {
			viewKilled = append(viewKilled, tmp.UserName)
		}
	}

	if len(viewKilled) > 0 {
		wd.SendTTLMsg(fmt.Sprintf("Убитые:\n%s", strings.Join(viewKilled, "\n")), "", chatID, Buttons{}, time.Second*10)
	} else {
		wd.SendTTLMsg("Убитых нет", "", chatID, Buttons{}, time.Second*10)
	}
}

func (wd *Telega) russianRoulette(chatID int64, msg *tgbotapi.Message) {
	players := []*UserInfo{wd.CastUserToUserinfo(msg.From)}

	if wd.UserIsAdmin(msg.Chat.ChatConfig(), msg.From.ID) && !wd.UserIsCreator(msg.Chat.ChatConfig(), msg.From.ID) {
		wd.SendTTLMsg("Администраторы не могут играть", "", chatID, Buttons{}, time.Second*5)
		return
	}

	var msgID int
	var author int64
	buttons := Buttons{
		{
			caption: "Продолжить",
			handler: func(update *tgbotapi.Update, button *Button) bool {
				from := wd.GetUser(update)
				if from.ID != author {
					wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Вопрос не для вас")
					return false
				}

				wd.DeleteMessage(chatID, msgID)

				randPlayer := wd.GetRandUser(chatID, msg.From.ID)
				if randPlayer == nil {
					wd.SendTTLMsg("Не смог получить оппонента", "", chatID, Buttons{}, time.Second*5)
					return true
				}

				players = append(players, randPlayer)

				wd.SendTTLMsg(fmt.Sprintf("Игра против %s.", randPlayer.Name), "", chatID, Buttons{}, time.Second*10)
				time.Sleep(time.Millisecond * 500)

				id := rand.Intn(len(players))
				player1, player2 := players[id], players[(id+1)%len(players)]

				if !shot(wd, msg.Chat, player1) {
					time.Sleep(time.Millisecond * 500)
					if !shot(wd, msg.Chat, player2) {
						wd.SendTTLMsg("Ура, никто не умер", "", chatID, Buttons{}, time.Second*5)
					}
				}

				return true
			},
		},
		{
			caption: "Отмена",
			handler: func(update *tgbotapi.Update, button *Button) bool {
				from := wd.GetUser(update)
				if from.ID != author {
					wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Вопрос не для вас")
					return false
				}

				wd.DeleteMessage(chatID, msgID)
				return true
			},
		},
	}

	m, err := wd.SendMsg("Русская рулетка.\n"+
		"После нажатия на кнопку «Продолжить» случайным образом выбирается один участник чата. \n"+
		"Далее происходит два «выстрела»: один в тебя, другой — в выбранного участника. \n"+
		"Вероятность «попасть под пулю» составляет 1/6 \n"+
		"Если игрок «получает пулю», ему устанавливается режим только для чтения (RO) на 24 часа (администраторы имеют иммунитет). \n"+
		"Если после двух «выстрелов» никто не попадает под пулю, игра завершается, и никто не выбывает.", "", chatID, buttons)

	if err == nil {
		msgID = m.MessageID
		author = msg.From.ID
	} else {
		log.Println(errors.Wrap(err, "sendMsg error"))
	}
}
