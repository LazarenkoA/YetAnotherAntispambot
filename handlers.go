package app

import (
	"encoding/json"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/k0kubun/pp/v3"
	"github.com/pkg/errors"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	errAlreadySelected = errors.New("moderator already selected")
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
		wd.SendMsg("Некорректный формат сообщения", "", chatID, Buttons{})
		return
	}

	if authKey == "" {
		wd.SendMsg("Не определен authKey для giga chat", "", chatID, Buttons{})
		return
	}

	analysis, err := wd.gigaClient(chatID, authKey).GetMessageCharacteristics(split[1])
	if err != nil {
		wd.SendMsg(fmt.Sprintf("Произошла ошибка: %s", err.Error()), "", chatID, Buttons{})
	} else {
		mypp := pp.New()
		mypp.SetColoringEnabled(false)
		wd.SendMsg(fmt.Sprintf("%v", mypp.Sprint(analysis)), "", chatID, Buttons{})
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

func (wd *Telega) randomModerator(chatID int64, msg *tgbotapi.Message, deadline time.Time) error {
	if userName, _ := wd.GetActiveRandModerator(chatID); userName != "" {
		return errAlreadySelected
	}

	randUser := wd.GetRandUserByWeight(chatID, msg.From.ID)
	if randUser == nil {
		return errors.New("не смог получить кандидата")
	}

	wd.logger.Debug(fmt.Sprintf("выбран случайный пользователь %q", randUser.Name))

	if wd.UserIsAdmin(msg.Chat.ChatConfig(), randUser.ID) {
		return fmt.Errorf("%s уже является администратором, можно попробовать повторно выбрать кандидатуру", randUser.Name)
	}

	if err := wd.AppointModerator(chatID, randUser, deadline); err != nil {
		return err
	} else {
		wd.SendMsg(fmt.Sprintf("У нас новый модератор (%s), срок до %s (UTC)", randUser.Name, deadline.UTC().Format("02-01-2006 15:04")), "", chatID, Buttons{})
	}

	return nil
}

func (wd *Telega) randomModeratorAutoExtend(chatID int64, msg *tgbotapi.Message) {
	if userName, deadline := wd.GetActiveRandModerator(chatID); userName != "" {
		wd.SendTTLMsg(fmt.Sprintf("%s уже выбран модератором, перевыбрать можно после %s (UTC)", userName, deadline.UTC().Format("02-01-2006 15:04:05")), "", chatID, Buttons{}, time.Second*5)
	}

	if wd.randomModeratorMX.TryLock() {
		defer wd.randomModeratorMX.Unlock()
	} else {
		wd.logger.Warn("выбор модератора уже запущен")
		return
	}

	tick := time.NewTicker(time.Minute * 5)
	defer tick.Stop()

	for {
		if err := wd.randomModerator(chatID, msg, time.Now().Add(time.Hour*24)); err != nil && !errors.Is(err, errAlreadySelected) {
			wd.logger.Error(errors.Wrap(err, "произошла ошибка при выборе модератора").Error())
			return
		}

		select {
		case <-wd.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (wd *Telega) russianRouletteKilled(chatID int64) {
	killed, err := wd.r.Items(killedUsers)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "redis read error").Error())
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
		wd.logger.Error(errors.Wrap(err, "sendMsg error").Error())
	}
}

func (wd *Telega) allChats(msg *tgbotapi.Message) {
	if msg == nil || ownerID == "" || strconv.FormatInt(msg.From.ID, 10) != ownerID {
		return
	}

	var links []string
	for _, item := range wd.getAllChats() {
		s := strings.Split(item, "::")
		chatIDStr, name := s[0], s[1]

		chatID, _ := strconv.ParseInt(chatIDStr, 10, 64)

		chat, err := wd.bot.GetChat(tgbotapi.ChatInfoConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
		})
		if err == nil {
			links = append(links, fmt.Sprintf("%s (@%s)", name, chat.UserName))
		}

		//inviteLinkConfig := tgbotapi.ChatInviteLinkConfig{
		//	ChatConfig: tgbotapi.ChatConfig{
		//		ChatID:             chatID,
		//		SuperGroupUsername: name,
		//	},
		//}
		//
		//inviteLink, err := wd.bot.GetInviteLink(inviteLinkConfig)
		//if err != nil {
		//	wd.logger.With("chatID", chatIDStr, "name", name).Error(errors.Wrap(err, "GetInviteLink error").Error())
		//} else {
		//	inviteLink = "<>"
		//}

	}

	wd.SendMsg(strings.Join(links, "\n"), "", msg.Chat.ID, Buttons{})
}

func (wd *Telega) notify(chatID int64, msg *tgbotapi.Message) {
	if msg == nil || ownerID == "" || strconv.FormatInt(msg.From.ID, 10) != ownerID {
		return
	}

	split := strings.Split(msg.Text, "::")
	if len(split) != 2 {
		wd.SendMsg("Некорректный формат сообщения", "", chatID, Buttons{})
		return
	}

	msgText := split[1]

	for _, item := range wd.getAllChats() {
		s := strings.Split(item, "::")
		if chatID, err := strconv.ParseInt(s[0], 10, 64); err == nil {
			if _, err := wd.SendMsg(msgText, "", chatID, Buttons{}); err == nil {
				wd.logger.Info(fmt.Sprintf("в чат %q отправлено сообщение", s[1]))
			}
		}
	}
}
