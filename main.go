package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

type wrapper struct {
	settings map[string]string
	err      error
}

var (
	BotToken   = os.Getenv("BotToken")
	WebhookURL = os.Getenv("WebhookURL")
	port       = os.Getenv("PORT")
	redisaddr  = os.Getenv("REDIS")
)

const (
	questionskey = "questions"
)

func init() {

}

func main() {
	if BotToken == "" {
		fmt.Println("в переменных окружения не задан BotToken")
		os.Exit(1)
	}
	if port == "" {
		fmt.Println("в переменных окружения не задан PORT")
		os.Exit(1)
	}
	if redisaddr == "" {
		fmt.Println("в переменных окружения не задан адрес redis")
		os.Exit(1)
	}

	wd := new(Telega)
	wdUpdate, err := wd.New()
	if err != nil {
		fmt.Println("не удалось подключить бота, ошибка:\n", err.Error())
		os.Exit(1)
	}

	for update := range wdUpdate {
		msg := wd.GetMessage(update)
		if msg == nil {
			continue
		}

		chatID := msg.Chat.ID
		me, _ := wd.bot.GetMe()

		// обработка команд кнопок
		if wd.CallbackQuery(update) {
			continue
		}

		command := wd.GetMessage(update).Command()
		switch command {
		case "start":
			txt := fmt.Sprintf("Привет %s %s\n"+
				"Что бы начать пользоваться ботом нужно выполнить следующие действия:\n"+
				"1. Добавить бота в нужную группу\n"+
				"2. Выдать боту админские права\n"+
				"3. Выполнить в боте команду /configuration, выбрать чат и загрузить (отправить файл) конфиг. "+
				"Пример конфига можно скачать выполнив команду /exampleconf", msg.From.FirstName, msg.From.LastName)
			wd.SendMsg(txt, "", chatID, Buttons{})
			continue
		case "configuration":
			key := strconv.Itoa(wd.GetMessage(update).From.ID)
			if wd.r.KeyExists(key) {
				configuration(wd, update, chatID)
			} else {
				wd.SendMsg("Для вас не найден активный чат, видимо вы не добавили бота в чат.", "", chatID, Buttons{})
			}
			continue
		case "exampleconf":
			if f, err := ioutil.TempFile("", "*.yaml"); err == nil {
				f.WriteString(confExample())
				f.Close()

				wd.SendFile(chatID, f.Name())
				os.RemoveAll(f.Name())
			}
			continue
		case "test":
			// для теста
			msg.NewChatMembers = &[]tgbotapi.User{
				{ID: 22, FirstName: "test", LastName: "test", UserName: "test", LanguageCode: "", IsBot: false},
			}
			continue
		case "help":
			msg := fmt.Sprintf("Антиспам для групп, при входе нового участника в группу бот задает вопрос, если ответа нет, участник блокируется.\n"+
				"Если нужно заблокировать пользователя тегните сообщения ботом (ответить на сообщение с текстом @%s).", me.UserName)
			wd.SendMsg(msg, "", chatID, Buttons{})
			continue
		default:
			if command != "" {
				wd.SendMsg("Команда "+command+" не поддерживается", "", chatID, Buttons{})
				continue
			} else {
				key := strconv.FormatInt(chatID, 10)
				if call, ok := wd.hooks[key]; ok {
					if call(update) {
						wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    chatID,
							MessageID: msg.MessageID})

						delete(wd.hooks, key)
					}
				}
			}
		}

		conf := readCoinf(wd, chatID)
		newChatMembers := msg.NewChatMembers
		if newChatMembers != nil {
			for _, user := range *newChatMembers {
				handlerAddNewMembers(wd, update, user, conf)
			}
		}

		if msg.LeftChatMember != nil && msg.LeftChatMember.ID == me.ID {
			wd.r.DeleteItems(strconv.Itoa(wd.GetMessage(update).From.ID), strconv.FormatInt(chatID, 10))
		}
		if msg.Text == "@"+strings.Trim(me.UserName, " ") && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && conf != nil {
			wd.StartVoting(msg, chatID, conf.CountVoted)
		}
	}
}

func configuration(wd *Telega, update tgbotapi.Update, chatID int64) {
	buttons := Buttons{}
	chats, _ := wd.r.Items(strconv.Itoa(wd.GetMessage(update).From.ID))
	key := strconv.FormatInt(chatID, 10)

	var msg *tgbotapi.Message
	handler := func(_ *tgbotapi.Update, b *Button) bool {
		chat := b.ID
		buttons := Buttons{
			&Button{
				caption: "Скачать",
				handler: func(*tgbotapi.Update, *Button) bool {
					if !getSettings(wd, chat, chatID) {
						wd.EditMsg(msg, "Для данного чата не найдено настроек", buttons)
					}
					return false
				},
			},
		}

		wd.EditMsg(msg, "Что бы загрузить настройки отправьте файл <b>yaml</b>\n"+
			"<pre>Пример настроек можно посмотреть в репазитории https://github.com/LazarenkoA/YetAnotherAntispambot</pre>\n"+
			"Что бы скачать текущие настройки нажмите \"Скачать\"", buttons)

		wd.hooks[key] = func(upd tgbotapi.Update) bool {
			if upd.Message == nil {
				return false
			}
			if confdata, err := wd.ReadFile(upd.Message); err == nil {
				settings, err := wrap(wd.r.StringMap(questionskey)).result()
				if err != nil {
					wd.SendMsg(fmt.Sprintf("Произошла ошибка при получении значения из redis:\n%v", err), "", chatID, Buttons{})
				}

				settings[chat] = confdata
				wd.r.SetMap(questionskey, settings)

				return true
			} else {
				return false
			}
		}

		return false
	}

	for _, chat := range chats {
		caption, err := wd.r.Get(chat)
		if err != nil || caption == "" {
			caption = chat
		}
		buttons = append(buttons, &Button{
			caption: caption,
			handler: handler,
			ID:      chat,
		})
	}

	msg, _ = wd.SendMsg("выберите чат", "", chatID, buttons)
}

func getSettings(wd *Telega, key string, chatID int64) bool {
	settings, err := wrap(wd.r.StringMap(questionskey)).result()
	if err != nil {
		log.Println(fmt.Errorf("ошибка получения конфига из redis: %w", err))
	}

	if s, ok := settings[key]; ok {
		if f, err := ioutil.TempFile("", "*.yaml"); err == nil {
			defer os.RemoveAll(f.Name())
			f.WriteString(s)
			f.Close()

			if err := wd.SendFile(chatID, f.Name()); err != nil {
				return false
			}
		}
	} else {
		return false
	}
	return true
}

func handlerAddNewMembers(wd *Telega, update tgbotapi.Update, appendedUser tgbotapi.User, conf *Conf) {
	var message *tgbotapi.Message

	chat := wd.GetMessage(update).Chat
	parentMsgID := wd.GetMessage(update).MessageID

	// когда добавили бота в чат, проверяем является ли он админом, если нет, сообщаем что нужно добавить в группу
	me, _ := wd.bot.GetMe()
	if appendedUser.ID == me.ID {
		if !wd.MeIsAdmin(chat.ChatConfig()) {
			message, _ := wd.SendMsg("Для корректной работы сделайте меня администратором", "", chat.ID, Buttons{})

			// отслеживаем статус, когда сделают администратором удаляем сообщение
			go func() {
				tick := time.NewTicker(time.Second)
				for range tick.C {
					if wd.MeIsAdmin(chat.ChatConfig()) {
						wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    chat.ID,
							MessageID: message.MessageID})

						tick.Stop()
					}
				}
			}()

			strChatID := strconv.FormatInt(chat.ID, 10)
			wd.r.AppendItems(strconv.Itoa(wd.GetMessage(update).From.ID), strChatID)
			wd.r.Set(strChatID, chat.Title, -1)
		}
		return
	}

	if conf == nil {
		log.Printf("для чата %s %s (%s) не определены настройки\n", chat.FirstName, chat.LastName, chat.UserName)
		return
	}

	wd.DisableSendMessages(chat.ID, &appendedUser, 0) // ограничиваем пользователя писать сообщения пока он не ответит верно на вопрос

	handlers := []func(*tgbotapi.Update, *Button) bool{}
	deleteMessage := func() {
		if _, err := wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
			ChatID:    wd.GetMessage(update).Chat.ID,
			MessageID: message.MessageID}); err == nil {
			wd.r.DeleteItems(keyActiveMSG, strconv.Itoa(message.MessageID))
		}
	}
	handlercancel := func(update *tgbotapi.Update, _ *Button) (result bool) {
		from := wd.GetUser(update)
		if result = update == nil || from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from); result {
			deleteMessage()
			wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chat.ID,
				MessageID: parentMsgID})
			wd.KickChatMember(appendedUser, tgbotapi.KickChatMemberConfig{
				ChatMemberConfig: tgbotapi.ChatMemberConfig{
					ChatID:             chat.ID,
					SuperGroupUsername: "",
					ChannelUsername:    "",
					UserID:             appendedUser.ID,
				},
				UntilDate: 0,
			})
		}
		return result
	}

	ctx, cancel := context.WithCancel(context.Background())

	b := Buttons{}
	for _, ans := range conf.Answers {
		a := ans // для замыкания
		handlers = append(handlers, func(update *tgbotapi.Update, currentButton *Button) (result bool) {
			from := wd.GetUser(update)
			if result = from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from); result {
				if a.Correct {
					cancel()
					wd.EnableWritingMessages(chat.ID, &appendedUser)
					deleteMessage()
				} else {
					deleteMessage()
					wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
						ChatID:    chat.ID,
						MessageID: parentMsgID})
					wd.KickChatMember(appendedUser, tgbotapi.KickChatMemberConfig{
						ChatMemberConfig: tgbotapi.ChatMemberConfig{
							ChatID:             chat.ID,
							SuperGroupUsername: "",
							ChannelUsername:    "",
							UserID:             appendedUser.ID,
						},
						UntilDate: 0,
					})
				}
			} else {
				wd.bot.AnswerCallbackQuery(tgbotapi.CallbackConfig{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Вопрос не для вас",
					ShowAlert:       true,
				})
			}
			return result
		})

		b = append(b, &Button{
			caption: ans.Txt,
			handler: handlers[len(handlers)-1],
		})
	}

	caption := "Не знаю"
	timeout := 60
	if conf.KickCaption != "" {
		caption = conf.KickCaption
	}
	if conf.Timeout > 0 {
		timeout = conf.Timeout
	}
	b = append(b, &Button{
		caption: caption,
		handler: handlercancel,
		timer:   timeout,
	})

	txt := fmt.Sprintf("Привет %s %s\nДля проверки на антиспам просьба ответить на вопрос:"+
		"\n%s", appendedUser.FirstName, appendedUser.LastName, conf.Question.Txt)
	message, _ = wd.ReplyMsg(txt, conf.Question.Img, chat.ID, b, wd.GetMessage(update).MessageID)
	wd.r.AppendItems(keyActiveMSG, strconv.Itoa(message.MessageID))

	// вместо таймера на кнопке
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second * time.Duration(timeout)):
			handlercancel(&update, nil)
			cancel()
		}
	}()
}

func readCoinf(wd *Telega, chatID int64) *Conf {
	settings, err := wrap(wd.r.StringMap(questionskey)).result()
	if err != nil {
		wd.SendMsg(fmt.Sprintf("Произошла ошибка при получении значения из redis:\n%v", err), "", chatID, Buttons{})
	}

	key := strconv.FormatInt(chatID, 10)
	confStr, ok := settings[key]
	if !ok {
		return nil
	}

	conf, err := LoadConf([]byte(confStr))
	if err != nil {
		return nil
	}
	return conf
}

func wrap(settings map[string]string, err error) *wrapper {
	return &wrapper{
		settings: settings,
		err:      err,
	}
}

func (w *wrapper) result() (map[string]string, error) {
	if w.err == nil {
		return w.settings, nil
	} else {
		return map[string]string{}, w.err
	}
}
